"use client";

import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { DocPage } from "@/features/docs/content";

export function DocView({ doc }: { doc: DocPage }) {
  return (
    <article className="prose-console max-w-3xl rounded-lg border bg-card p-6">
      <ReactMarkdown remarkPlugins={[remarkGfm]}>{doc.body}</ReactMarkdown>
    </article>
  );
}
