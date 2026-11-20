import { parseIpv4 } from "@/lib/validation";

const LABEL = /^(?!-)[a-zA-Z0-9_-]{1,63}(?<!-)$/;

export function validateDomainName(name: string): string | null {
  const n = name.trim().replace(/\.$/, "");
  if (!n) return "Enter a domain name.";
  if (n.length > 253) return "Domain names must be 253 characters or fewer.";
  if (!n.split(".").every((l) => l === "*" || LABEL.test(l))) return "Use letters, digits and hyphens separated by dots (e.g. example.internal).";
  return null;
}

export function validateRecordValues(type: string, values: string[]): string | null {
  if (values.length === 0) return "Enter at least one value.";
  if (type === "A") {
    const bad = values.find((v) => parseIpv4(v) === null);
    if (bad) return `"${bad}" is not a valid IPv4 address.`;
  }
  if (type === "CNAME") {
    if (values.length > 1) return "A CNAME record can have only one value.";
    const e = validateDomainName(values[0]);
    if (e) return `CNAME target: ${e}`;
  }
  if (type === "TXT" && values.some((v) => v.length > 255)) return "Each TXT value must be 255 characters or fewer.";
  return null;
}
