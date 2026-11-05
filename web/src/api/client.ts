import type {
  ApproveInput,
  ChecklistCandidate,
  IngestionJob,
  InventoryCard,
  InventoryFilter,
  PipelineStats,
  ReviewItem,
  StorageLocation,
  JobStatus,
} from "./types";

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, init);
  if (!res.ok) {
    let message = `${res.status} ${res.statusText}`;
    try {
      const body = (await res.json()) as { error?: string };
      if (body.error) message = body.error;
    } catch {
      // non-JSON error body; keep the status text
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

const json = (body: unknown): RequestInit => ({
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

export const api = {
  stats: () =>
    request<{ queue: Partial<Record<JobStatus, number>>; pipeline?: PipelineStats }>("/api/v1/stats"),
  reviewQueue: () => request<{ items: ReviewItem[]; count: number }>("/api/v1/review-queue"),
  approve: (jobId: string, input: ApproveInput) =>
    request<InventoryCard>(`/api/v1/review-queue/${encodeURIComponent(jobId)}/approve`, json(input)),
  reject: (jobId: string, reason: string) =>
    request<void>(`/api/v1/review-queue/${encodeURIComponent(jobId)}/reject`, json({ reason })),
  inventory: (f: InventoryFilter) => {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(f)) if (v) params.set(k, String(v));
    params.set("limit", "1000");
    return request<{ items: InventoryCard[]; count: number }>(`/api/v1/inventory?${params}`);
  },
  storage: () => request<{ items: StorageLocation[] }>("/api/v1/storage"),
  searchChecklist: (q: string) =>
    request<{ items: ChecklistCandidate[] }>(`/api/v1/checklist/search?q=${encodeURIComponent(q)}`),
  upload: (front: File, back?: File) => {
    const form = new FormData();
    form.append("front", front);
    if (back) form.append("back", back);
    return request<IngestionJob>("/api/v1/upload", { method: "POST", body: form });
  },
};

export const formatCents = (cents: number) =>
  (cents / 100).toLocaleString("en-US", { style: "currency", currency: "USD" });
