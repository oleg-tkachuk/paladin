import Link from "next/link";

export default function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center min-h-[60vh] animate-fade-in px-6">
      <div className="text-8xl font-bold text-white/5 mb-4 select-none tracking-tighter">
        404
      </div>
      <h1 className="text-2xl font-bold text-white mb-3 tracking-tight">
        Page Not Found
      </h1>
      <p className="text-sm text-slate-400 text-center max-w-md mb-8">
        The resource you are looking for does not exist or has been moved.
      </p>
      <Link
        href="/"
        className="px-6 py-3 rounded-2xl bg-indigo-600 hover:bg-indigo-500 text-white text-sm font-bold transition-all shadow-lg shadow-indigo-600/20 active:scale-95"
      >
        Return to Dashboard
      </Link>
    </div>
  );
}
