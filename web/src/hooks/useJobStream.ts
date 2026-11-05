import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type { EventType, JobStatus, PipelineEvent, PipelineStats } from "../api/types";

const EVENT_TYPES: EventType[] = [
  "job.queued",
  "job.status",
  "job.completed",
  "job.needs_review",
  "job.failed",
  "card.approved",
  "queue.stats",
];

export interface StreamState {
  connected: boolean;
  queue: Partial<Record<JobStatus, number>>;
  stats?: PipelineStats;
  recent: PipelineEvent[]; // newest first, capped
}

/** Subscribes to the SSE scanner feed and keeps query caches fresh as jobs move. */
export function useJobStream(): StreamState {
  const qc = useQueryClient();
  const [state, setState] = useState<StreamState>({ connected: false, queue: {}, recent: [] });
  const invalidateTimer = useRef<number | undefined>(undefined);

  useEffect(() => {
    const es = new EventSource("/api/v1/jobs/stream");
    const scheduleInvalidate = (keys: string[][]) => {
      // Coalesce bursts (a batch of 50 scans) into one refetch.
      window.clearTimeout(invalidateTimer.current);
      invalidateTimer.current = window.setTimeout(() => {
        for (const key of keys) void qc.invalidateQueries({ queryKey: key });
      }, 400);
    };
    const onEvent = (e: MessageEvent<string>) => {
      let ev: PipelineEvent;
      try {
        ev = JSON.parse(e.data) as PipelineEvent;
      } catch {
        return;
      }
      setState((s) => {
        const next: StreamState = { ...s, connected: true };
        if (ev.type === "queue.stats") {
          next.queue = ev.queue ?? {};
          if (ev.stats) next.stats = ev.stats;
        } else {
          next.recent = [ev, ...s.recent].slice(0, 30);
        }
        return next;
      });
      if (ev.type === "job.needs_review" || ev.type === "card.approved" || ev.type === "job.failed") {
        scheduleInvalidate([["review-queue"], ["inventory"], ["storage"]]);
      } else if (ev.type === "job.completed") {
        scheduleInvalidate([["inventory"], ["storage"]]);
      }
    };
    for (const t of EVENT_TYPES) es.addEventListener(t, onEvent as EventListener);
    es.onopen = () => setState((s) => ({ ...s, connected: true }));
    es.onerror = () => setState((s) => ({ ...s, connected: false })); // EventSource retries itself
    return () => {
      window.clearTimeout(invalidateTimer.current);
      es.close();
    };
  }, [qc]);

  return state;
}
