FROM python:3.11-slim

WORKDIR /app

# System deps for OpenCV
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1-mesa-glx libglib2.0-0 libsm6 libxext6 libxrender-dev libgomp1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Pre-download YOLO models
RUN python -c "from ultralytics import YOLO; YOLO('yolov8n.pt'); YOLO('yolov8n-pose.pt')"

COPY main.py .

EXPOSE 9000

CMD ["python", "main.py"]
