"""
Detector Sidecar — 4-stage safety inspection engine.

Stage 1: YOLO object detection + pose estimation
Stage 2: Rule-based spatial reasoning (IoU checks)
Stage 3: Traditional CV structural analysis (Canny + Hough)
Stage 4: Frame differencing + optical flow motion analysis

Returns structured DetectorResult JSON consumed by Go safeagent.
"""

import base64
import io
import logging
import time
from typing import Optional

import cv2
import numpy as np
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel, Field
from ultralytics import YOLO

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger("detector")

app = FastAPI(title="SafeAgent Detector Sidecar")

# ── Models (lazy-loaded) ──────────────────────────────────────────────────

_detect_model: Optional[YOLO] = None
_pose_model: Optional[YOLO] = None


def get_detect_model() -> YOLO:
    global _detect_model
    if _detect_model is None:
        logger.info("Loading YOLOv8n detection model...")
        _detect_model = YOLO("yolov8n.pt")
        logger.info("Detection model loaded.")
    return _detect_model


def get_pose_model() -> YOLO:
    global _pose_model
    if _pose_model is None:
        logger.info("Loading YOLOv8n-pose model...")
        _pose_model = YOLO("yolov8n-pose.pt")
        logger.info("Pose model loaded.")
    return _pose_model


# ── Pydantic schemas ──────────────────────────────────────────────────────

class DetectRequest(BaseModel):
    image: str = Field(..., description="Base64-encoded JPEG image")
    mime_type: str = Field(default="image/jpeg")
    prev_image: Optional[str] = Field(default=None, description="Previous frame for diff analysis")


class DetectedObj(BaseModel):
    id: str
    type: str
    confidence: float
    bbox: list[int]  # [x1, y1, x2, y2]


class Violation(BaseModel):
    type: str
    severity: str
    person_id: str = ""
    confidence: float
    reason: str
    regulation: str = ""


class StructuralHazard(BaseModel):
    type: str
    line: list[list[int]] = []
    has_guardrail: bool = False


class MotionAnalysis(BaseModel):
    diff_ratio: float
    alerts: list[str] = []


class DetectResponse(BaseModel):
    status: str  # "safe" | "violation" | "suspicious"
    objects: list[DetectedObj] = []
    violations: list[Violation] = []
    structural_hazards: list[StructuralHazard] = []
    motion: MotionAnalysis = Field(default_factory=MotionAnalysis)
    should_invoke_vlm: bool = False
    summary: str = ""


# ── Helpers ────────────────────────────────────────────────────────────────

def decode_image(b64: str) -> np.ndarray:
    raw = base64.b64decode(b64)
    arr = np.frombuffer(raw, np.uint8)
    img = cv2.imdecode(arr, cv2.IMREAD_COLOR)
    if img is None:
        raise ValueError("Failed to decode image")
    return img


def iou(box_a: list[int], box_b: list[int]) -> float:
    """Intersection over Union for two [x1,y1,x2,y2] boxes."""
    xa = max(box_a[0], box_b[0])
    ya = max(box_a[1], box_b[1])
    xb = min(box_a[2], box_b[2])
    yb = min(box_a[3], box_b[3])
    inter = max(0, xb - xa) * max(0, yb - ya)
    area_a = (box_a[2] - box_a[0]) * (box_a[3] - box_a[1])
    area_b = (box_b[2] - box_b[0]) * (box_b[3] - box_b[1])
    union = area_a + area_b - inter
    return inter / union if union > 0 else 0.0


# ── Stage 1: YOLO Detection ───────────────────────────────────────────────

COCO_CLASSES = {
    0: "person", 1: "bicycle", 2: "car", 3: "motorcycle", 5: "bus", 7: "truck",
    # Safety-related (we fine-tune a custom model for helmet/vest/harness in production)
}

SAFETY_CLASSES = {"person"}


def run_yolo_detection(img: np.ndarray) -> list[DetectedObj]:
    model = get_detect_model()
    results = model(img, verbose=False)
    objects: list[DetectedObj] = []
    if results and results[0].boxes:
        for i, box in enumerate(results[0].boxes):
            cls_id = int(box.cls[0].item())
            cls_name = COCO_CLASSES.get(cls_id, model.names.get(cls_id, f"class_{cls_id}"))
            conf = float(box.conf[0].item())
            if conf < 0.3:
                continue
            xyxy = box.xyxy[0].tolist()
            objects.append(DetectedObj(
                id=f"{cls_name}_{i}",
                type=cls_name,
                confidence=round(conf, 3),
                bbox=[int(v) for v in xyxy],
            ))
    return objects


def run_pose_estimation(img: np.ndarray) -> list[dict]:
    """Returns list of {person_idx, keypoints: [[x,y,conf]*17]}."""
    model = get_pose_model()
    results = model(img, verbose=False)
    poses = []
    if results and results[0].keypoints:
        for i, kpts in enumerate(results[0].keypoints):
            if kpts.conf is None:
                continue
            confs = kpts.conf[0].tolist() if hasattr(kpts.conf[0], 'tolist') else [float(c) for c in kpts.conf[0]]
            avg_conf = sum(confs) / len(confs) if confs else 0
            if avg_conf < 0.3:
                continue
            xy = kpts.xy[0].tolist() if hasattr(kpts.xy[0], 'tolist') else [[float(v) for v in pt] for pt in kpts.xy[0]]
            poses.append({
                "person_idx": i,
                "keypoints": [[float(v) for v in pt] for pt in xy],
                "confidences": confs,
            })
    return poses


# ── Stage 2: Rule Engine ──────────────────────────────────────────────────

def check_helmet(person: DetectedObj, all_objects: list[DetectedObj]) -> Optional[Violation]:
    """Check if a helmet bbox overlaps with the person's head region."""
    px1, py1, px2, py2 = person.bbox
    head_h = (py2 - py1) / 3
    head_region = [px1, py1, px2, int(py1 + head_h)]

    helmets = [o for o in all_objects if o.type in ("helmet", "hardhat", "hat")]
    for h in helmets:
        if iou(head_region, h.bbox) > 0.2:
            return None  # helmet found on head

    return Violation(
        type="no_helmet",
        severity="high",
        person_id=person.id,
        confidence=0.92,
        reason=f"person {person.id} head region has no helmet overlap",
        regulation="JGJ 80-2016 高处作业安全技术规范",
    )


def check_vest(person: DetectedObj, all_objects: list[DetectedObj]) -> Optional[Violation]:
    """Check if a safety vest bbox overlaps with the person's torso."""
    px1, py1, px2, py2 = person.bbox
    torso_h = (py2 - py1) / 3
    torso_region = [px1, int(py1 + torso_h), px2, int(py2 - torso_h)]

    vests = [o for o in all_objects if o.type in ("vest", "reflective_vest", "safety_vest")]
    for v in vests:
        if iou(torso_region, v.bbox) > 0.2:
            return None

    return Violation(
        type="no_vest",
        severity="medium",
        person_id=person.id,
        confidence=0.85,
        reason=f"person {person.id} torso has no safety vest overlap",
        regulation="JGJ 184-2009 建筑施工作业劳动防护用品配备标准",
    )


def check_harness(person: DetectedObj, all_objects: list[DetectedObj]) -> Optional[Violation]:
    """Check if a harness bbox overlaps with the person's torso."""
    px1, py1, px2, py2 = person.bbox
    torso_h = (py2 - py1) / 3
    torso_region = [px1, int(py1 + torso_h), px2, int(py2 - torso_h)]

    harnesses = [o for o in all_objects if o.type in ("harness", "safety_belt")]
    for h in harnesses:
        if iou(torso_region, h.bbox) > 0.15:
            return None

    return Violation(
        type="no_harness",
        severity="critical",
        person_id=person.id,
        confidence=0.88,
        reason=f"person {person.id} torso has no harness overlap",
        regulation="JGJ 80-2016 高处作业安全技术规范",
    )


def check_edge_proximity(person: DetectedObj, edge_zones: list[list[int]]) -> Optional[Violation]:
    """Check if a person bbox overlaps with any edge danger zone."""
    for zone in edge_zones:
        if iou(person.bbox, zone) > 0.1:
            return Violation(
                type="near_edge",
                severity="critical",
                person_id=person.id,
                confidence=0.85,
                reason=f"person {person.id} is within edge danger zone",
                regulation="JGJ 80-2016 高处作业安全技术规范",
            )
    return None


def run_rules(objects: list[DetectedObj], edge_zones: list[list[int]]) -> list[Violation]:
    violations: list[Violation] = []
    persons = [o for o in objects if o.type == "person"]
    for person in persons:
        if v := check_helmet(person, objects):
            violations.append(v)
        if v := check_vest(person, objects):
            violations.append(v)
        if v := check_harness(person, objects):
            violations.append(v)
        if v := check_edge_proximity(person, edge_zones):
            violations.append(v)
    return violations


# ── Stage 3: Traditional CV ────────────────────────────────────────────────

def detect_edges_and_holes(img: np.ndarray) -> tuple[list[StructuralHazard], list[list[int]]]:
    """Detect floor edges and openings using Canny + HoughLinesP."""
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    edges = cv2.Canny(gray, 50, 150)
    lines = cv2.HoughLinesP(edges, 1, np.pi / 180, threshold=80, minLineLength=100, maxLineGap=30)

    hazards: list[StructuralHazard] = []
    edge_zones: list[list[int]] = []

    if lines is not None:
        for line in lines:
            x1, y1, x2, y2 = line[0]
            # Only consider near-horizontal lines (floor edges)
            angle = abs(np.arctan2(y2 - y1, x2 - x1) * 180 / np.pi)
            if angle < 20 or angle > 160:
                # Check for guardrail (vertical lines above the edge)
                rail_region = img[max(0, y1 - 80):y1, x1:x2] if y1 > 0 else None
                has_rail = False
                if rail_region is not None and rail_region.size > 0:
                    rail_gray = cv2.cvtColor(rail_region, cv2.COLOR_BGR2GRAY) if len(rail_region.shape) == 3 else rail_region
                    rail_edges = cv2.Canny(rail_gray, 50, 150)
                    rail_lines = cv2.HoughLinesP(rail_edges, 1, np.pi / 2, threshold=30, minLineLength=30, maxLineGap=10)
                    has_rail = rail_lines is not None and len(rail_lines) >= 2

                hazards.append(StructuralHazard(
                    type="unprotected_edge" if not has_rail else "protected_edge",
                    line=[[int(x1), int(y1)], [int(x2), int(y2)]],
                    has_guardrail=has_rail,
                ))

                if not has_rail:
                    # Create danger zone: 50px inward from the edge
                    edge_zones.append([x1, y1 - 50, x2, y1 + 50])

    return hazards, edge_zones


# ── Stage 4: Motion Analysis ──────────────────────────────────────────────

def analyze_motion(img: np.ndarray, prev_img: Optional[np.ndarray]) -> MotionAnalysis:
    if prev_img is None:
        return MotionAnalysis(diff_ratio=0.0, alerts=[])

    # 4a: Pixel-level frame difference
    gray = cv2.cvtColor(img, cv2.COLOR_BGR2GRAY)
    prev_gray = cv2.cvtColor(prev_img, cv2.COLOR_BGR2GRAY)

    diff = cv2.absdiff(gray, prev_gray)
    _, thresh = cv2.threshold(diff, 30, 255, cv2.THRESH_BINARY)
    diff_ratio = round(np.count_nonzero(thresh) / thresh.size, 4)

    alerts: list[str] = []
    if diff_ratio < 0.05:
        alerts.append("scene_static")

    # 4b: Optical flow (Farneback)
    flow = cv2.calcOpticalFlowFarneback(prev_gray, gray, None, 0.5, 3, 15, 3, 5, 1.2, 0)
    mag, ang = cv2.cartToPolar(flow[..., 0], flow[..., 1])

    # Check for large downward motion (potential fall)
    downward_mask = (ang > np.pi * 0.4) & (ang < np.pi * 0.6) & (mag > 5)
    if np.count_nonzero(downward_mask) > 500:
        alerts.append("rapid_downward_motion")

    # Check for large horizontal motion (potential running/climbing)
    horizontal_mask = ((ang < np.pi * 0.2) | (ang > np.pi * 0.8)) & (mag > 5)
    if np.count_nonzero(horizontal_mask) > 1000:
        alerts.append("rapid_horizontal_motion")

    return MotionAnalysis(diff_ratio=diff_ratio, alerts=alerts)


# ── Decision Fusion ────────────────────────────────────────────────────────

def decide_status(violations: list[Violation], motion: MotionAnalysis) -> tuple[str, bool]:
    """Determine final status and whether VLM should be invoked."""
    if not violations and not motion.alerts:
        return "safe", False

    critical_count = sum(1 for v in violations if v.severity == "critical")
    high_count = sum(1 for v in violations if v.severity == "high")

    if critical_count > 0 or high_count >= 2:
        return "violation", False  # clear violation, no VLM needed

    if violations and all(v.severity in ("low", "medium") for v in violations):
        return "violation", False

    # Complex motion patterns → suspicious, need VLM
    if "rapid_downward_motion" in motion.alerts or "rapid_horizontal_motion" in motion.alerts:
        return "suspicious", True

    return "suspicious", True  # ambiguous, let VLM decide


# ── Routes ─────────────────────────────────────────────────────────────────

@app.get("/health")
def health():
    return {"status": "ok"}


@app.post("/detect", response_model=DetectResponse)
def detect(req: DetectRequest):
    t0 = time.time()
    try:
        img = decode_image(req.image)
        prev_img = decode_image(req.prev_image) if req.prev_image else None
    except Exception as e:
        raise HTTPException(status_code=400, detail=f"Image decode error: {e}")

    # Stage 1: YOLO
    objects = run_yolo_detection(img)
    poses = run_pose_estimation(img)

    # Stage 3: Traditional CV
    structural_hazards, edge_zones = detect_edges_and_holes(img)

    # Stage 2: Rules (uses Stage 1 + Stage 3 results)
    violations = run_rules(objects, edge_zones)

    # Stage 4: Motion
    motion = analyze_motion(img, prev_img)

    # Decision fusion
    status, should_invoke_vlm = decide_status(violations, motion)

    # Build summary
    parts = []
    if objects:
        parts.append(f"检测到{len(objects)}个目标")
    if violations:
        parts.append(f"发现{len(violations)}项违规")
    if structural_hazards:
        unprotected = sum(1 for h in structural_hazards if not h.has_guardrail)
        if unprotected:
            parts.append(f"发现{unprotected}处未防护临边")
    if not parts:
        parts.append("场景安全，未发现异常")

    elapsed = (time.time() - t0) * 1000
    logger.info(f"Detection complete: status={status}, vlm={should_invoke_vlm}, objects={len(objects)}, "
                f"violations={len(violations)}, edges={len(structural_hazards)}, {elapsed:.1f}ms")

    return DetectResponse(
        status=status,
        objects=objects,
        violations=violations,
        structural_hazards=structural_hazards,
        motion=motion,
        should_invoke_vlm=should_invoke_vlm,
        summary="；".join(parts),
    )


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=9000)
