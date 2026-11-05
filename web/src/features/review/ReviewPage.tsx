import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { api } from "../../api/client";
import { useHotkeys } from "../../hooks/useHotkeys";
import { ScanViewer } from "./ScanViewer";
import { ReviewForm } from "./ReviewForm";

export function ReviewPage() {
  const qc = useQueryClient();
  const queue = useQuery({ queryKey: ["review-queue"], queryFn: api.reviewQueue });
  const items = queue.data?.items ?? [];
  const [index, setIndex] = useState(0);
  const [flash, setFlash] = useState<string>();

  useEffect(() => {
    if (index >= items.length && items.length > 0) setIndex(items.length - 1);
  }, [items.length, index]);

  useHotkeys({
    j: () => setIndex((i) => Math.min(items.length - 1, i + 1)),
    k: () => setIndex((i) => Math.max(0, i - 1)),
  });

  const advance = (message: string) => {
    setFlash(message);
    // The finished job drops out of the queue, so the same index now shows the next card.
    void qc.invalidateQueries({ queryKey: ["review-queue"] });
    void qc.invalidateQueries({ queryKey: ["inventory"] });
    void qc.invalidateQueries({ queryKey: ["storage"] });
  };

  if (queue.isPending) return <p className="p-6 text-muted">Loading the review queue…</p>;
  if (queue.isError) return <p className="p-6 text-bad">Couldn’t load the review queue: {queue.error.message}</p>;

  const item = items[index];
  if (!item) {
    return (
      <div className="mx-auto max-w-prose p-10">
        <h1 className="font-display text-2xl font-semibold">Nothing to review</h1>
        <p className="mt-2 text-muted">
          Every scan cleared the 85% confidence bar. Cards the OCR isn’t sure about will land here as the scanner runs.
        </p>
        {flash && <p className="mt-4 text-good">{flash}</p>}
      </div>
    );
  }
  const confidence = Math.round(item.payload.confidence_score * 100);

  return (
    <div className="grid h-full min-h-0 grid-cols-1 lg:grid-cols-[minmax(0,1.1fr)_minmax(26rem,0.9fr)]">
      <ScanViewer key={item.job.id} item={item} />
      <div className="flex min-h-0 flex-col border-l border-rule bg-panel/60">
        <div className="flex items-center gap-3 border-b border-rule px-5 py-2.5">
          <button type="button" className="btn-quiet px-1.5" onClick={() => setIndex((i) => Math.max(0, i - 1))} disabled={index === 0} aria-label="Previous card (K)">
            <ChevronLeft size={16} />
          </button>
          <span className="tnum text-sm">
            Card {index + 1} of {items.length}
          </span>
          <button
            type="button"
            className="btn-quiet px-1.5"
            onClick={() => setIndex((i) => Math.min(items.length - 1, i + 1))}
            disabled={index >= items.length - 1}
            aria-label="Next card (J)"
          >
            <ChevronRight size={16} />
          </button>
          <span className="text-xs text-muted">
            <span className="kbd">J</span> <span className="kbd">K</span>
          </span>
          <span className="ml-auto text-sm">
            <span className="text-muted">OCR confidence </span>
            <span className={`tnum font-display text-lg font-semibold ${confidence < 85 ? "text-amber" : "text-good"}`}>{confidence}%</span>
          </span>
        </div>
        {item.job.error_message && <p className="border-b border-rule px-5 py-2 text-sm text-muted">Why it’s here: {item.job.error_message}</p>}
        {flash && (
          <p className="border-b border-rule px-5 py-1.5 text-sm text-good" role="status">
            {flash}
          </p>
        )}
        <ReviewForm
          key={item.job.id}
          item={item}
          onApproved={(sku) => advance(`Approved ${sku}`)}
          onRejected={() => advance("Scan rejected")}
        />
      </div>
    </div>
  );
}
