interface Props {
  title: string
  icon: string
}

export function PlaceholderPage({ title, icon }: Props) {
  return (
    <div className="flex flex-col items-center justify-center h-full text-center pt-32">
      <div className="text-6xl mb-4">{icon}</div>
      <p className="text-xl font-medium text-gray-500">{title}</p>
      <p className="text-sm text-gray-400 mt-1">功能开发中，敬请期待</p>
    </div>
  )
}
