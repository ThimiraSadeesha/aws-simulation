/**
 * Client-side validation mirroring the backend rules documented in docs/api.md.
 * The backend is always authoritative; these give fast inline feedback.
 * Every validator returns a map of field → message (empty map = valid).
 */
export type FieldErrors = Record<string, string>;

export const hasErrors = (e: FieldErrors) => Object.keys(e).length > 0;

// ---------------- Auth ----------------
export const USERNAME_RE = /^[a-zA-Z0-9_.-]{3,32}$/;

export function validateRegister(v: { username: string; password: string; confirm?: string; email?: string }): FieldErrors {
  const e: FieldErrors = {};
  if (!USERNAME_RE.test(v.username)) e.username = "3–32 characters: letters, digits, '_', '.', '-'.";
  if (v.password.length < 8) e.password = "Password must be at least 8 characters.";
  if (v.confirm !== undefined && v.confirm !== v.password) e.confirm = "Passwords do not match.";
  if (v.email && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(v.email)) e.email = "Enter a valid email address.";
  return e;
}

// ---------------- IPv4 / CIDR ----------------
export function parseIpv4(ip: string): number | null {
  const parts = ip.trim().split(".");
  if (parts.length !== 4) return null;
  let n = 0;
  for (const p of parts) {
    if (!/^\d{1,3}$/.test(p)) return null;
    const v = Number(p);
    if (v > 255) return null;
    if (p.length > 1 && p.startsWith("0")) return null;
    n = n * 256 + v;
  }
  return n >>> 0;
}

export interface ParsedCidr {
  network: number;
  prefix: number;
  size: number;
}

export function parseCidr(cidr: string): ParsedCidr | null {
  const m = /^(\d{1,3}(?:\.\d{1,3}){3})\/(\d{1,2})$/.exec(cidr.trim());
  if (!m) return null;
  const ip = parseIpv4(m[1]);
  const prefix = Number(m[2]);
  if (ip === null || prefix > 32) return null;
  const size = 2 ** (32 - prefix);
  return { network: ip, prefix, size };
}

/** True when the address has no host bits set for its prefix. */
export function isNetworkAligned(c: ParsedCidr): boolean {
  return c.network % c.size === 0;
}

export function cidrContains(outer: ParsedCidr, inner: ParsedCidr): boolean {
  return inner.prefix >= outer.prefix && inner.network >= outer.network && inner.network + inner.size <= outer.network + outer.size;
}

export function cidrOverlaps(a: ParsedCidr, b: ParsedCidr): boolean {
  return a.network < b.network + b.size && b.network < a.network + a.size;
}

export function isRfc1918(c: ParsedCidr): boolean {
  const ranges = ["10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"].map((r) => parseCidr(r)!);
  return ranges.some((r) => cidrContains(r, c));
}

/** Generic IPv4 CIDR check (any prefix 0–32), used by SG / NACL / route editors. */
export function validateCidrFormat(cidr: string): string | null {
  const c = parseCidr(cidr);
  if (!c) return "Enter an IPv4 CIDR such as 10.0.0.0/16.";
  if (!isNetworkAligned(c)) return "Host bits must be zero (e.g. 10.0.1.0/24, not 10.0.1.5/24).";
  return null;
}

export interface CidrCheck {
  error: string | null;
  warning: string | null;
}

/** VPC CIDR: prefix /16 – /28, RFC1918 recommended (warning only). */
export function validateVpcCidr(cidr: string): CidrCheck {
  const fmt = validateCidrFormat(cidr);
  if (fmt) return { error: fmt, warning: null };
  const c = parseCidr(cidr)!;
  if (c.prefix < 16 || c.prefix > 28) return { error: "VPC CIDR prefix must be between /16 and /28.", warning: null };
  return {
    error: null,
    warning: isRfc1918(c) ? null : "Not an RFC 1918 private range (10/8, 172.16/12, 192.168/16) – allowed, but not recommended.",
  };
}

/** Subnet CIDR: /16 – /28, within the VPC CIDR, not overlapping sibling subnets. */
export function validateSubnetCidr(cidr: string, vpcCidr?: string, siblings: { cidr_block: string; subnet_id?: string }[] = []): string | null {
  const fmt = validateCidrFormat(cidr);
  if (fmt) return fmt;
  const c = parseCidr(cidr)!;
  if (c.prefix < 16 || c.prefix > 28) return "Subnet CIDR prefix must be between /16 and /28.";
  if (vpcCidr) {
    const v = parseCidr(vpcCidr);
    if (v && !cidrContains(v, c)) return `Subnet CIDR must be within the VPC CIDR ${vpcCidr}.`;
  }
  for (const s of siblings) {
    const o = parseCidr(s.cidr_block);
    if (o && cidrOverlaps(o, c)) return `Overlaps existing subnet ${s.subnet_id ?? ""} (${s.cidr_block}).`.replace("  ", " ");
  }
  return null;
}

// ---------------- S3 ----------------
/** DNS-compatible bucket names, 3–63 chars. */
export function validateBucketName(name: string): string | null {
  if (name.length < 3 || name.length > 63) return "Bucket names must be 3–63 characters long.";
  if (!/^[a-z0-9.-]+$/.test(name)) return "Use only lowercase letters, digits, dots (.) and hyphens (-).";
  if (!/^[a-z0-9]/.test(name) || !/[a-z0-9]$/.test(name)) return "Bucket names must begin and end with a letter or digit.";
  if (name.includes("..")) return "Bucket names must not contain two adjacent periods.";
  if (name.includes(".-") || name.includes("-.")) return "Bucket names must not contain '.-' or '-.'.";
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(name)) return "Bucket names must not be formatted as an IP address.";
  if (name.startsWith("xn--")) return "Bucket names must not start with 'xn--'.";
  return null;
}

// ---------------- EC2 ----------------
export const MAX_LAUNCH_COUNT = 20;
export const MAX_USER_DATA_BYTES = 16 * 1024;

export interface LaunchForm {
  name: string;
  image_id: string;
  instance_type: string;
  count: number;
  subnet_id: string;
  security_group_ids: string[];
  key_name: string;
  user_data: string;
  tags: { key: string; value: string }[];
}

export function validateTags(tags: { key: string; value: string }[]): string | null {
  const seen = new Set<string>();
  for (const t of tags) {
    const k = t.key.trim();
    if (!k && !t.value) continue;
    if (!k) return "Tag keys must not be empty.";
    if (k.length > 128) return `Tag key "${k.slice(0, 20)}…" is longer than 128 characters.`;
    if (t.value.length > 256) return `Tag value for "${k}" is longer than 256 characters.`;
    if (seen.has(k)) return `Duplicate tag key "${k}".`;
    seen.add(k);
  }
  return null;
}

export function validateLaunch(f: LaunchForm, ctx: { subnetVpcId?: string; sgVpcIds?: Record<string, string> } = {}): FieldErrors {
  const e: FieldErrors = {};
  if (!f.image_id) e.image_id = "Choose an image (AMI).";
  if (!f.instance_type) e.instance_type = "Choose an instance type.";
  if (!Number.isInteger(f.count) || f.count < 1 || f.count > MAX_LAUNCH_COUNT)
    e.count = `Number of instances must be a whole number from 1 to ${MAX_LAUNCH_COUNT}.`;
  if (f.name.length > 255) e.name = "Name must be 255 characters or fewer.";
  if (new TextEncoder().encode(f.user_data).length > MAX_USER_DATA_BYTES) e.user_data = "User data must be 16 KiB or smaller.";
  const tagErr = validateTags(f.tags);
  if (tagErr) e.tags = tagErr;
  if (f.subnet_id && ctx.subnetVpcId && ctx.sgVpcIds) {
    const wrong = f.security_group_ids.filter((id) => ctx.sgVpcIds![id] && ctx.sgVpcIds![id] !== ctx.subnetVpcId);
    if (wrong.length) e.security_group_ids = `Security group(s) ${wrong.join(", ")} belong to a different VPC than the subnet.`;
  }
  if (!f.subnet_id && f.security_group_ids.length && ctx.sgVpcIds) {
    // Without a subnet the default VPC is used – SGs must then be default-VPC groups; backend decides.
  }
  return e;
}

export const KEY_PAIR_NAME_RE = /^[\w.\-@ ]{1,255}$/;

// ---------------- Security group / NACL ----------------
export function validatePortRange(protocol: string, from: number, to: number): string | null {
  if (protocol === "-1" || protocol === "icmp") return null;
  if (!Number.isInteger(from) || !Number.isInteger(to) || from < 0 || to > 65535) return "Ports must be between 0 and 65535.";
  if (from > to) return "From-port must be less than or equal to to-port.";
  return null;
}

// ---------------- IAM ----------------
export const IAM_NAME_RE = /^[\w+=,.@-]{1,64}$/;
export function validateIamName(name: string, what = "Name"): string | null {
  if (!IAM_NAME_RE.test(name)) return `${what} must be 1–64 characters: letters, digits and + = , . @ _ -`;
  return null;
}

/** Parse JSON text into a policy object; returns error text for malformed JSON. */
export function parsePolicyJson(text: string): { value: Record<string, unknown> | null; error: string | null } {
  try {
    const v = JSON.parse(text) as unknown;
    if (!v || typeof v !== "object" || Array.isArray(v)) return { value: null, error: "Policy must be a JSON object." };
    return { value: v as Record<string, unknown>, error: null };
  } catch (err) {
    return { value: null, error: `Invalid JSON: ${(err as Error).message}` };
  }
}

// ---------------- CloudWatch ----------------
export const COMPARISON_OPERATORS = [
  "GreaterThanThreshold",
  "GreaterThanOrEqualToThreshold",
  "LessThanThreshold",
  "LessThanOrEqualToThreshold",
] as const;
export const STATISTICS = ["Average", "Maximum", "Minimum", "Sum", "SampleCount"] as const;

export interface AlarmForm {
  alarm_name: string;
  namespace: string;
  metric_name: string;
  dimension_key: string;
  dimension_value: string;
  statistic: string;
  period: string;
  evaluation_periods: string;
  threshold: string;
  comparison_operator: string;
  alarm_actions: string;
}

export function validateAlarm(f: AlarmForm): FieldErrors {
  const e: FieldErrors = {};
  if (!f.alarm_name.trim()) e.alarm_name = "Alarm name is required.";
  else if (f.alarm_name.length > 255) e.alarm_name = "Alarm name must be 255 characters or fewer.";
  if (!f.namespace.trim()) e.namespace = "Namespace is required.";
  if (!f.metric_name.trim()) e.metric_name = "Metric name is required.";
  if ((f.dimension_key && !f.dimension_value) || (!f.dimension_key && f.dimension_value))
    e.dimension_value = "Provide both a dimension name and value, or neither.";
  if (!(STATISTICS as readonly string[]).includes(f.statistic)) e.statistic = "Choose a statistic.";
  const period = Number(f.period);
  if (!Number.isInteger(period) || period < 10 || (period >= 60 && period % 60 !== 0) || (period < 60 && ![10, 30].includes(period)))
    e.period = "Period must be 10, 30 or a multiple of 60 seconds.";
  const ep = Number(f.evaluation_periods);
  if (!Number.isInteger(ep) || ep < 1 || ep > 100) e.evaluation_periods = "Evaluation periods must be a whole number from 1 to 100.";
  if (f.threshold.trim() === "" || Number.isNaN(Number(f.threshold))) e.threshold = "Threshold must be a number.";
  if (!(COMPARISON_OPERATORS as readonly string[]).includes(f.comparison_operator)) e.comparison_operator = "Choose a comparison operator.";
  for (const a of f.alarm_actions.split(/[\s,]+/).filter(Boolean)) {
    if (!a.startsWith("arn:cloudsim:")) {
      e.alarm_actions = `"${a}" is not a CloudSim ARN (arn:cloudsim:…).`;
      break;
    }
  }
  return e;
}

export function validateNamespaceForPublish(ns: string): string | null {
  if (!ns.trim()) return "Namespace is required.";
  if (ns.startsWith("CloudSim/")) return "Custom metric namespaces must not start with 'CloudSim/'.";
  if (ns.length > 255) return "Namespace must be 255 characters or fewer.";
  return null;
}
