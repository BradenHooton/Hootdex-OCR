import { useEffect, useState } from "react";
import { StatusBar, type View } from "./components/StatusBar";
import { ReviewPage } from "./features/review/ReviewPage";
import { InventoryPage } from "./features/inventory/InventoryPage";
import { UploadPanel } from "./features/upload/UploadPanel";
import { useJobStream } from "./hooks/useJobStream";
import { useHotkeys } from "./hooks/useHotkeys";

const VIEWS: View[] = ["review", "inventory", "upload"];

function viewFromHash(): View {
  const h = window.location.hash.replace("#", "") as View;
  return VIEWS.includes(h) ? h : "review";
}

export default function App() {
  const stream = useJobStream();
  const [view, setView] = useState<View>(viewFromHash);

  useEffect(() => {
    const onHash = () => setView(viewFromHash());
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  const go = (v: View) => {
    window.location.hash = v;
    setView(v);
  };
  useHotkeys({ "alt+1": () => go("review"), "alt+2": () => go("inventory"), "alt+3": () => go("upload") });

  return (
    <div className="flex h-screen min-h-0 flex-col">
      <StatusBar stream={stream} view={view} onNavigate={go} />
      <main className="min-h-0 flex-1">
        {view === "review" && <ReviewPage />}
        {view === "inventory" && <InventoryPage />}
        {view === "upload" && <UploadPanel recent={stream.recent} />}
      </main>
    </div>
  );
}
