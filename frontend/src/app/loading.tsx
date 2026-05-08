import { Skeleton } from "@/components/ui/Skeleton";

export default function GlobalLoading() {
  return (
    <div className="space-y-8 animate-fade-in p-6 sm:p-8 lg:p-10">
      <Skeleton className="h-10 w-64 rounded-2xl" />
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
        {Array.from({ length: 6 }).map((_, i) => (
          <Skeleton key={i} className="h-40 rounded-[2rem]" />
        ))}
      </div>
    </div>
  );
}
