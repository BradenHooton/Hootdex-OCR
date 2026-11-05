import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { ImagePlus } from "lucide-react";
import { api } from "../../api/client";
import type { PipelineEvent } from "../../api/types";

export function UploadPanel(props: { recent: PipelineEvent[] }) {
  const [front, setFront] = useState<File>();
  const [back, setBack] = useState<File>();
  const upload = useMutation({
    mutationFn: () => {
      if (!front) throw new Error("Choose a front image first.");
      return api.upload(front, back);
    },
  });

  const submit = (e: FormEvent) => {
    e.preventDefault();
    upload.mutate();
  };

  return (
    <div className="mx-auto grid max-w-5xl gap-8 p-6 md:grid-cols-[1fr_1fr]">
      <form onSubmit={submit} className="space-y-4">
        <h1 className="font-display text-2xl font-semibold">Add a card by hand</h1>
        <p className="max-w-prose text-sm text-muted">
          For cards that skipped the scanner. Upload the front, and the back if you have it — they go through the same
          OCR pipeline as hot-folder scans.
        </p>
        <FilePick label="Front image" file={front} onChange={setFront} required />
        <FilePick label="Back image (optional)" file={back} onChange={setBack} />
        <button type="submit" className="btn-primary" disabled={!front || upload.isPending}>
          <ImagePlus size={16} aria-hidden /> {upload.isPending ? "Uploading…" : "Upload scan"}
        </button>
        {upload.isSuccess && (
          <p className="text-sm text-good">Uploaded. Job {upload.data.id.slice(0, 8)} is in the queue.</p>
        )}
        {upload.isError && <p className="text-sm text-bad">Upload failed: {upload.error.message}</p>}
      </form>

      <section aria-labelledby="feed-title">
        <h2 id="feed-title" className="mb-3 font-display text-lg font-semibold">
          Scanner feed
        </h2>
        {props.recent.length === 0 ? (
          <p className="text-sm text-muted">Nothing yet. Scans dropped into the inbox folder appear here as they move.</p>
        ) : (
          <ol className="divide-y divide-rule border-y border-rule text-sm">
            {props.recent.map((ev, i) => (
              <li key={`${ev.at}-${i}`} className="flex gap-3 py-1.5">
                <time className="tnum shrink-0 text-muted">{new Date(ev.at).toLocaleTimeString()}</time>
                <span className="truncate">
                  {ev.type.replace(".", " ")} {ev.sku ?? ev.status?.toLowerCase() ?? ""} {ev.message ?? ""}
                </span>
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  );
}

function FilePick(props: { label: string; file?: File; onChange: (f?: File) => void; required?: boolean }) {
  return (
    <label className="block text-sm">
      <span className="mb-1 block text-muted">{props.label}</span>
      <input
        type="file"
        accept="image/jpeg,image/png,image/tiff"
        required={props.required}
        onChange={(e) => props.onChange(e.target.files?.[0])}
        className="block w-full text-sm file:mr-3 file:rounded-sm file:border file:border-rule file:bg-raised file:px-3 file:py-1.5 file:text-ink"
      />
    </label>
  );
}
