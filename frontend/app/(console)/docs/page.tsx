import { DOCS } from "@/features/docs/content";
import { DocView } from "./doc-view";

export const metadata = { title: "Documentation" };

export default function DocsIndex() {
  return <DocView doc={DOCS[0]} />;
}
