// Types mirroring docs/api.md (the REST contract). Field names are snake_case as on the wire.

export type Tags = Record<string, string>;

export interface ListResponse<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

export interface ListParams {
  q?: string;
  sort?: string;
  order?: "asc" | "desc";
  page?: number;
  page_size?: number;
  [filter: string]: string | number | undefined;
}

export interface ApiErrorBody {
  error: { code: string; message: string; request_id?: string };
}

// ---------- Auth ----------
export interface User {
  id: number;
  username: string;
  email: string;
  role: "admin" | "user";
  account_id: string;
  created_at: string;
}
export interface SessionResponse {
  user: User;
  csrf_token: string;
}

// ---------- Dashboard / events / audit ----------
export interface CloudEvent {
  id: number;
  ts: string;
  resource_id: string;
  resource_type: string;
  type: string;
  message: string;
  detail?: Record<string, unknown>;
}
export interface AuditEntry {
  id: number;
  ts: string;
  principal: string;
  action: string;
  resource: string;
  result: "allowed" | "denied" | "error" | string;
  status_code: number;
  request_id: string;
  source_ip: string;
}
export interface DatedValue {
  ts: string;
  value: number;
}
export interface ActiveLab {
  lab_id: string;
  title: string;
  completed_steps: number;
  total_steps: number;
}
export interface DashboardSummary {
  resources_total: number;
  instances: { running: number; stopped: number; pending: number; total: number; [state: string]: number };
  buckets: number;
  objects: number;
  storage_bytes: number;
  vpcs: number;
  load_balancers: number;
  autoscaling_groups: number;
  alarms: { ok: number; alarm: number; insufficient_data: number };
  avg_cpu_percent: number;
  network_in_bytes_5m: number;
  network_out_bytes_5m: number;
  cpu_series: DatedValue[];
  recent_events: CloudEvent[];
  recent_activity: AuditEntry[];
  health: { backend: string; engine: string; database: string };
  active_labs: ActiveLab[];
}
export interface HealthResponse {
  status: string;
  database: string;
  engine: string;
  version?: string;
}
export interface MetricSample {
  resource_id: string;
  owner?: string;
  cpu_percent: number;
  memory_percent: number;
  network_in_bytes: number;
  network_out_bytes: number;
  disk_read_bytes: number;
  disk_write_bytes: number;
}
export type EventsWsMessage =
  | { type: "event"; event: CloudEvent }
  | { type: "metrics"; samples: MetricSample[] };

// ---------- EC2 ----------
export type InstanceState =
  | "pending"
  | "running"
  | "stopping"
  | "stopped"
  | "rebooting"
  | "shutting-down"
  | "terminated";
export interface Instance {
  instance_id: string;
  name: string;
  state: InstanceState;
  state_reason: string;
  instance_type: string;
  vcpu: number;
  memory_mib: number;
  image_id: string;
  key_name: string;
  vpc_id: string;
  subnet_id: string;
  private_ip: string;
  public_ip: string;
  private_dns: string;
  security_group_ids: string[];
  tags: Tags;
  user_data: string;
  workload_profile: WorkloadProfile;
  autoscaling_group: string;
  launch_time: string;
  created_at: string;
  updated_at: string;
}
export type WorkloadProfile = "idle" | "steady" | "spiky" | "stress" | "custom";
export interface RunInstancesRequest {
  image_id: string;
  instance_type: string;
  count?: number;
  name?: string;
  key_name?: string;
  subnet_id?: string;
  security_group_ids?: string[];
  associate_public_ip?: boolean;
  user_data?: string;
  tags?: Tags;
}
export interface InstanceType {
  name: string;
  vcpu: number;
  memory_mib: number;
  family: string;
  hourly_price_sim: number;
}
export interface MachineImage {
  image_id: string;
  name: string;
  os: string;
  description: string;
  architecture: string;
}
export interface KeyPair {
  key_pair_id: string;
  name: string;
  fingerprint: string;
  created_at: string;
}
export interface CreatedKeyPair extends KeyPair {
  private_key_pem: string;
}
export interface ConsoleOutput {
  output: string;
  ts: string;
}

// ---------- S3 ----------
export interface LifecycleRule {
  id: string;
  status: "Enabled" | "Disabled";
  prefix: string;
  expiration_days?: number;
  noncurrent_expiration_days?: number;
  transitions?: { days: number; storage_class: string }[];
  abort_incomplete_multipart_days?: number;
}
export interface Bucket {
  name: string;
  region: string;
  created_at: string;
  versioning: "Disabled" | "Enabled" | "Suspended";
  acl: "private" | "public-read";
  object_count: number;
  size_bytes: number;
  has_policy: boolean;
  lifecycle_rules: LifecycleRule[];
}
export interface S3Object {
  key: string;
  version_id: string;
  is_latest: boolean;
  size: number;
  etag: string;
  content_type: string;
  last_modified: string;
  storage_class: string;
  metadata: Record<string, string>;
  delete_marker: boolean;
}
export interface ObjectListResponse extends ListResponse<S3Object> {
  common_prefixes: string[];
}
export interface S3Stats {
  buckets: number;
  objects: number;
  versions: number;
  size_bytes: number;
  by_bucket: { name: string; objects: number; size_bytes: number }[];
  by_storage_class: Record<string, number>;
}

// ---------- VPC ----------
export interface Vpc {
  vpc_id: string;
  name: string;
  cidr_block: string;
  is_default: boolean;
  enable_dns_support: boolean;
  enable_dns_hostnames: boolean;
  state: string;
  tags: Tags;
  created_at: string;
}
export interface Subnet {
  subnet_id: string;
  vpc_id: string;
  name: string;
  cidr_block: string;
  availability_zone: string;
  map_public_ip_on_launch: boolean;
  available_ip_count: number;
  route_table_id: string;
  network_acl_id: string;
  is_public: boolean;
  tags: Tags;
}
export interface Route {
  destination_cidr: string;
  target_id: string;
  state: "active" | "blackhole";
}
export interface RouteTable {
  route_table_id: string;
  vpc_id: string;
  name: string;
  is_main: boolean;
  routes: Route[];
  subnet_ids: string[];
}
export interface InternetGateway {
  internet_gateway_id: string;
  name: string;
  vpc_id: string | null;
  state: "detached" | "attached";
}
export interface NatGateway {
  nat_gateway_id: string;
  name: string;
  subnet_id: string;
  vpc_id: string;
  state: "pending" | "available" | "deleting" | "deleted";
  public_ip: string;
  private_ip: string;
}
export interface SecurityGroupRule {
  rule_id: string;
  protocol: string;
  from_port: number;
  to_port: number;
  cidr: string;
  source_security_group_id: string;
  description: string;
}
export interface SecurityGroup {
  group_id: string;
  group_name: string;
  description: string;
  vpc_id: string;
  ingress: SecurityGroupRule[];
  egress: SecurityGroupRule[];
}
export interface NewSecurityGroupRule {
  direction: "ingress" | "egress";
  protocol: "tcp" | "udp" | "icmp" | "-1";
  from_port: number;
  to_port: number;
  cidr?: string;
  source_security_group_id?: string;
  description?: string;
}
export interface NetworkAclEntry {
  rule_number: number;
  egress: boolean;
  protocol: string;
  from_port: number;
  to_port: number;
  cidr: string;
  action: "allow" | "deny";
}
export interface NetworkAcl {
  network_acl_id: string;
  vpc_id: string;
  is_default: boolean;
  entries: NetworkAclEntry[];
  subnet_ids: string[];
  name?: string;
}
export interface NetworkInterface {
  network_interface_id: string;
  subnet_id: string;
  vpc_id: string;
  private_ip: string;
  public_ip: string;
  security_group_ids: string[];
  attachment: { instance_id: string; device_index: number } | null;
  status: "in-use" | "available";
  description: string;
}
export interface DnsAnswer {
  type: string;
  value: string;
  ttl: number;
}
export interface DnsResolveResponse {
  name: string;
  answers: DnsAnswer[];
  source: string;
}
export interface ReachabilityRequest {
  source: string;
  destination: string;
  protocol: "tcp" | "udp" | "icmp" | "-1";
  port: number;
}
export interface ReachabilityHop {
  component: string;
  type: string;
  direction?: string;
  result: "allow" | "deny" | string;
  detail: string;
}
export interface ReachabilityResponse {
  reachable: boolean;
  hops: ReachabilityHop[];
  blocked_by: { component: string; type: string; detail: string } | null;
  explanation: string;
  simulated_latency_ms?: number;
}
export interface TopologyInstance {
  instance_id: string;
  name: string;
  state: string;
  subnet_id: string;
  private_ip: string;
  public_ip: string;
}
export interface Topology {
  vpc: Vpc;
  subnets: Subnet[];
  route_tables: RouteTable[];
  internet_gateways: InternetGateway[];
  nat_gateways: NatGateway[];
  instances: TopologyInstance[];
  load_balancers: Partial<LoadBalancer>[];
}

// ---------- IAM ----------
export type PolicyDocument = Record<string, unknown>;
export interface AttachedPolicy {
  policy_name: string;
  policy_arn?: string;
  arn?: string;
}
export interface AccessKey {
  access_key_id: string;
  status: "Active" | "Inactive";
  created_at?: string;
  last_used_at?: string | null;
}
export interface CreatedAccessKey {
  access_key_id: string;
  secret_access_key: string;
  status: string;
}
export interface IamUser {
  user_name: string;
  user_id?: string;
  arn: string;
  path: string;
  created_at: string;
  tags?: Tags;
  groups?: string[] | { group_name: string }[];
  attached_policies?: AttachedPolicy[];
  inline_policies?: (string | { policy_name: string; policy_document?: PolicyDocument })[];
  access_keys?: AccessKey[];
}
export interface IamGroup {
  group_name: string;
  arn: string;
  path?: string;
  created_at: string;
  members?: string[];
  users?: string[];
  attached_policies?: AttachedPolicy[];
  inline_policies?: (string | { policy_name: string })[];
}
export interface IamRole {
  role_name: string;
  arn: string;
  description?: string;
  max_session_duration?: number;
  assume_role_policy_document: PolicyDocument;
  created_at: string;
  attached_policies?: AttachedPolicy[];
  inline_policies?: (string | { policy_name: string })[];
}
export interface IamPolicy {
  policy_name: string;
  arn: string;
  description?: string;
  is_builtin?: boolean;
  builtin?: boolean;
  default_version_id?: string;
  attachment_count?: number;
  policy_document?: PolicyDocument;
  created_at?: string;
  updated_at?: string;
}
export interface SimulateRequest {
  principal_arn: string;
  action: string;
  resource_arn: string;
  context?: Record<string, unknown>;
}
export interface SimulateResponse {
  decision: "allowed" | "explicitDeny" | "implicitDeny";
  matched_statements: { policy: string; sid: string; effect: string }[];
}
export interface ValidatePolicyResponse {
  valid: boolean;
  errors: (string | { message: string; path?: string })[];
}

// ---------- ELB ----------
export interface HealthCheck {
  path: string;
  interval_s: number;
  healthy_threshold: number;
  unhealthy_threshold: number;
}
export interface Target {
  id: string;
  port: number;
  weight: number;
  health: "healthy" | "unhealthy" | "unused" | "initial";
  reason: string;
}
export interface TargetGroup {
  target_group_id: string;
  arn: string;
  name: string;
  protocol: string;
  port: number;
  vpc_id: string;
  algorithm: "round_robin" | "least_outstanding_requests" | "weighted_random";
  health_check: HealthCheck;
  targets: Target[];
}
export interface Listener {
  listener_id: string;
  protocol: string;
  port: number;
  default_target_group_id: string;
}
export interface LoadBalancer {
  load_balancer_id: string;
  arn: string;
  name: string;
  dns_name: string;
  scheme: "internet-facing" | "internal";
  state: "provisioning" | "active" | "deleting";
  vpc_id: string;
  subnet_ids: string[];
  security_group_ids: string[];
  listeners: Listener[];
}
export interface LbSimulationResult {
  total: number;
  served: number;
  dropped: number;
  per_target: { id: string; requests: number; percent: number; avg_latency_ms: number; p99_latency_ms: number }[];
  avg_latency_ms: number;
}

// ---------- Auto Scaling ----------
export interface LaunchTemplate {
  name: string;
  launch_template_id?: string;
  image_id: string;
  instance_type: string;
  security_group_ids?: string[];
  key_name?: string;
  user_data?: string;
  workload_profile?: WorkloadProfile;
  created_at?: string;
}
export interface AutoScalingGroup {
  name: string;
  arn: string;
  launch_template: string;
  min_size: number;
  max_size: number;
  desired_capacity: number;
  subnet_ids: string[];
  target_group_ids: string[];
  instances: { instance_id: string; state: string; health: string }[];
  status: string;
  created_at: string;
  cooldown_s?: number;
  health_check_type?: "EC2" | "ELB";
}
export type ScalingPolicy =
  | { name: string; type: "target_tracking"; target_value: number }
  | { name: string; type: "step"; threshold: number; comparison: string; adjustment: number };
export interface ScalingActivity {
  id: string | number;
  ts: string;
  description: string;
  cause: string;
  status: "Successful" | "Failed" | "InProgress";
}

// ---------- CloudWatch ----------
export interface MetricDescriptor {
  namespace: string;
  metric_name: string;
  dimensions: Record<string, string>;
}
export interface MetricDataResponse {
  label: string;
  datapoints: DatedValue[];
}
export type Statistic = "Average" | "Maximum" | "Minimum" | "Sum" | "SampleCount";
export type ComparisonOperator =
  | "GreaterThanThreshold"
  | "GreaterThanOrEqualToThreshold"
  | "LessThanThreshold"
  | "LessThanOrEqualToThreshold";
export interface AlarmInput {
  alarm_name: string;
  namespace: string;
  metric_name: string;
  dimensions: Record<string, string>;
  statistic: Statistic;
  period: number;
  evaluation_periods: number;
  threshold: number;
  comparison_operator: ComparisonOperator;
  alarm_actions?: string[];
}
export interface Alarm extends AlarmInput {
  state: "OK" | "ALARM" | "INSUFFICIENT_DATA";
  state_reason: string;
  state_updated_at: string;
  arn?: string;
}
export interface AlarmHistoryItem {
  ts: string;
  old_state?: string;
  new_state?: string;
  from?: string;
  to?: string;
  reason?: string;
  summary?: string;
}
export interface LogGroup {
  name: string;
  retention_days?: number | null;
  created_at?: string;
  stored_bytes?: number;
  arn?: string;
}
export interface LogStream {
  name: string;
  stream_name?: string;
  created_at?: string;
  last_event_ts?: string | null;
}
export interface LogEvent {
  ts: string;
  message: string;
  stream?: string;
  id?: string | number;
}

// ---------- Labs ----------
export type LabLevel = "beginner" | "intermediate" | "advanced";
export interface LabStep {
  index: number;
  title: string;
  instructions_md: string;
  hint: string;
  validation: { type: string; [k: string]: unknown };
}
export interface LabSummary {
  id: string;
  title: string;
  level: LabLevel;
  service: string;
  duration_min: number;
  description: string;
  status: "not_started" | "in_progress" | "completed";
  completed_steps: number;
  total_steps: number;
}
export interface LabStepProgress {
  index: number;
  status: "pending" | "passed" | "failed";
  message: string;
  checked_at: string | null;
}
export interface LabProgress {
  status: "not_started" | "in_progress" | "completed";
  started_at: string | null;
  completed_at: string | null;
  steps: LabStepProgress[];
}
export interface Lab {
  id: string;
  title: string;
  level: LabLevel;
  service: string;
  duration_min: number;
  description: string;
  steps: LabStep[];
  progress?: LabProgress | null;
}
export interface ValidateStepResponse {
  passed: boolean;
  message: string;
  details?: Record<string, unknown>;
}

// ---------- Additional services ----------
export interface Queue {
  name: string;
  url?: string;
  arn?: string;
  created_at?: string;
  approximate_number_of_messages?: number;
  messages_available?: number;
  messages_in_flight?: number;
  visibility_timeout?: number;
}
export interface QueueMessage {
  message_id: string;
  receipt_handle: string;
  body: string;
  sent_at?: string;
  receive_count?: number;
}
export interface Topic {
  name: string;
  arn?: string;
  created_at?: string;
  subscriptions?: Subscription[];
}
export interface Subscription {
  subscription_id?: string;
  id?: string;
  protocol: "sqs" | "log";
  endpoint: string;
}
export interface Secret {
  name: string;
  arn?: string;
  description?: string;
  created_at?: string;
  updated_at?: string;
  version_id?: string;
  value?: string;
  secret_string?: string;
}
export interface DynamoKeyDef {
  name: string;
  type: "S" | "N";
}
export interface DynamoTable {
  table_name: string;
  partition_key: DynamoKeyDef;
  sort_key?: DynamoKeyDef | null;
  item_count?: number;
  created_at?: string;
  status?: string;
}
export type DynamoItem = Record<string, unknown>;
export interface HostedZone {
  id: string;
  zone_id?: string;
  name: string;
  private: boolean;
  vpc_id?: string | null;
  record_count?: number;
  created_at?: string;
}
export interface DnsRecord {
  name: string;
  type: "A" | "CNAME" | "TXT";
  ttl: number;
  values: string[];
}

// ---------- Admin ----------
export interface EngineStats {
  uptime_seconds?: number;
  resources?: Record<string, number>;
  resources_total?: number;
  events_emitted?: number;
  pending_transitions?: number;
  active_faults?: number;
  ticks?: number;
  time_scale?: number;
  seed?: number;
  quotas?: Record<string, unknown>;
  [k: string]: unknown;
}
export interface Backup {
  file: string;
  size: number;
  created_at?: string;
}
export interface Fault {
  id: string;
  target: string | null;
  kind: string | null;
  type: "crash" | "degrade" | "start_failure";
  probability: number;
  duration_ms: number | null;
  created_at?: string;
}
export interface IntegrityResult {
  integrity_check: string;
  foreign_key_violations: unknown[];
}
