import { notFound } from "next/navigation";
import { DOCS, getDoc } from "@/features/docs/content";
import { DocView } from "../doc-view";

export function generateStaticParams() {
  return DOCS.map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  return { title: getDoc(slug)?.title ?? "Documentation" };
}

export default async function DocPageRoute({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const doc = getDoc(slug);
  if (!doc) notFound();
  return <DocView doc={doc} />;
}
