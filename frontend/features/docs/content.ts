/**
 * In-app user guide. Static content summarising how each service behaves in the simulator.
 * CLI examples follow the `cloudsim <service> <operation> [--options]` grammar; run `help` in the lab
 * terminal (or `cloudsim help`) for the authoritative syntax of your installed version.
 */
export interface DocPage {
  slug: string;
  title: string;
  summary: string;
  body: string;
}

const CLI_NOTE = `> CLI examples use the \`cloudsim <service> <operation>\` grammar shared by the CLI and the in-browser lab terminal.
> Run \`help\` in the terminal for the exact options supported by your release.`;

export const DOCS: DocPage[] = [
  {
    slug: "getting-started",
    title: "Getting started",
    summary: "What CloudSim is, accounts, authentication and the console layout.",
    body: `# Getting started

CloudSim is a **local, educational cloud simulator**. Every resource you create — instances, buckets, VPCs,
users — exists only inside the simulator. Nothing is ever created in a real cloud account and no real
compute, storage or network traffic is consumed.

## Accounts and sign-in
* Registering creates a user with its own simulated **12-digit account ID**. The first registered user becomes
  the platform **admin** (can use *Settings → Administration*).
* Signing in to the console makes you the **account root user**: full access to your own account.
* Programmatic access (CLI, scripts) uses **IAM user access keys**. Those requests are evaluated by the IAM
  policy engine (explicit *Deny* › *Allow* › implicit deny).

## Console tour
* **Left navigation** – services. Items marked *soon* are not implemented in this release.
* **Top bar** – search box (filters the list on the current page), live event indicator (WebSocket event
  stream), your account ID and the user menu (theme, sign-out).
* **Dashboard** – resource counts, CPU trend, alarms, recent events and audit activity, component health.

## Resource IDs
IDs mimic familiar prefixes plus 17 hex characters: \`i-\`, \`vpc-\`, \`subnet-\`, \`sg-\`, \`rtb-\`, \`igw-\`,
\`nat-\`, \`acl-\`, \`eni-\`, \`ami-\`, \`tg-\`, \`lb-\`, \`asg-\`. ARNs look like
\`arn:cloudsim:<service>:local:<account_id>:<type>/<id>\`.

## Command line
${CLI_NOTE}

\`\`\`bash
cloudsim configure                 # store an access key id / secret for a profile
cloudsim sts get-caller-identity   # who am I?
cloudsim ec2 describe-instances
\`\`\`
`,
  },
  {
    slug: "ec2",
    title: "EC2 – instances",
    summary: "Launching instances, lifecycle states, workloads, metadata and key pairs.",
    body: `# EC2 – virtual machines (simulated)

Instances are simulated by the CloudSim engine. They have a realistic **lifecycle** and generate
**metrics** from a workload profile, but no operating system actually runs.

## Lifecycle
| Action | Transition |
|---|---|
| launch | \`pending\` → \`running\` (≈3 s) |
| stop | \`running\` → \`stopping\` → \`stopped\` (≈2 s) |
| start | \`stopped\` → \`pending\` → \`running\` |
| reboot | \`running\` → \`rebooting\` → \`running\` |
| terminate | any → \`shutting-down\` → \`terminated\` |

Invalid actions (e.g. stopping a stopped instance) return **409 InvalidStateTransition**. The console polls
instances while they are in a transitional state.

## Launching
* **Image (AMI)** and **instance type** are required.
* Without a subnet, the account's **default VPC** is used (\`172.31.0.0/16\`, subnet \`172.31.0.0/20\`, an internet
  gateway and a default security group are created on first use).
* **User data** is parsed and echoed into the console output — it is *not executed*.
* **Key pairs**: the private key is shown **once** at creation and never stored by CloudSim.

## Workload profiles
\`idle\` (~2–5 % CPU), \`steady\` (20–35 %), \`spiky\` (bursts to 80–95 %), \`stress\` (90–100 %), \`custom\` (your CPU
target ±3 %). Change it on the instance's *Monitoring* tab to drive alarms and auto scaling.

## Metadata
The *Metadata* tab shows an IMDS-style tree (\`instance-id\`, \`local-ipv4\`, \`placement/availability-zone\`…).

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim ec2 run-instances --image-id ami-… --instance-type t3.micro --count 1
cloudsim ec2 describe-instances
cloudsim ec2 stop-instances --instance-ids i-…
cloudsim ec2 create-key-pair --key-name my-key
\`\`\`
`,
  },
  {
    slug: "s3",
    title: "S3 – object storage",
    summary: "Buckets, objects, versioning, policies and lifecycle rules.",
    body: `# S3 – object storage (simulated)

Objects are stored by the CloudSim backend on local disk.

* **Bucket names** are DNS-compatible, 3–63 characters, lowercase, and globally unique across the platform.
* **Folders** are just key prefixes (\`photos/2026/cat.jpg\`). The object browser groups keys by \`/\`.
* **Uploads** from the console use multipart/form-data; the CLI can use multipart uploads for large files.
  Max object size defaults to 100 MiB.
* **Versioning** (\`Enabled\` / \`Suspended\`): deleting without a version id creates a *delete marker*; older
  versions remain visible in the *Versions* tab.
* **ACL**: \`private\` or \`public-read\`.
* **Bucket policies** use the familiar JSON grammar; \`Principal\` supports \`"*"\` and \`{"CloudSim": "<arn>"}\`.
  They are evaluated together with IAM identity policies.
* **Lifecycle rules** (expiration, noncurrent expiration, transitions, abort incomplete multipart uploads) are
  applied by a background job every 60 s — or immediately with *Run lifecycle now*.
* Deleting a non-empty bucket fails with **BucketNotEmpty** unless you choose *force*.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim s3 mb s3://my-bucket
cloudsim s3 cp ./report.pdf s3://my-bucket/docs/report.pdf
cloudsim s3 ls s3://my-bucket/docs/
cloudsim s3api put-bucket-versioning --bucket my-bucket --status Enabled
\`\`\`
`,
  },
  {
    slug: "vpc",
    title: "VPC – networking",
    summary: "VPCs, subnets, routing, gateways, security groups, NACLs and the reachability analyzer.",
    body: `# VPC – virtual networking (simulated)

* A **VPC** has an IPv4 CIDR between /16 and /28 (RFC 1918 ranges recommended). Creating one also creates a
  *main route table* (with the \`local\` route), a *default network ACL* (allow all) and a *default security group*.
* **Subnets** must sit inside the VPC CIDR and must not overlap. A subnet is **public** when its route table has
  \`0.0.0.0/0 → igw-…\`.
* **Internet gateways** must be *attached* to a VPC to be used. **NAT gateways** live in a (public) subnet and
  give private subnets outbound-only internet access.
* **Security groups** are *stateful* (return traffic is allowed automatically). New groups allow all egress.
* **Network ACLs** are *stateless*, evaluated lowest rule number first, with an implicit deny. New non-default
  ACLs deny everything until you add entries; return traffic needs ephemeral ports 1024–65535.
* **DNS**: \`ip-10-0-1-10.cloudsim.internal\` names, \`<Name tag>.<vpc_id>.cloudsim.internal\` (when DNS hostnames
  are enabled) and Route 53 private zone records resolve inside the VPC.

## Reachability Analyzer
Pick a source and destination (instance, \`internet\` or an IP), a protocol and port. The engine walks the path
— source SG egress → source NACL outbound → route table → destination NACL inbound → destination SG ingress —
and reports each hop and the component that **blocked** the traffic, with a simulated latency.

## Topology
Each VPC page has a *Topology* tab that draws subnets (public / private), instances, internet and NAT gateways
and the routes between them.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim ec2 create-vpc --cidr-block 10.0.0.0/16
cloudsim ec2 create-subnet --vpc-id vpc-… --cidr-block 10.0.1.0/24
cloudsim ec2 authorize-security-group-ingress --group-id sg-… --protocol tcp --port 22 --cidr 0.0.0.0/0
\`\`\`
`,
  },
  {
    slug: "iam",
    title: "IAM – identity & access",
    summary: "Users, groups, roles, policies, access keys and the policy simulator.",
    body: `# IAM – identity and access management

* The console session is the **account root user**. Create **IAM users** for least-privilege access and give them
  **access keys** for the CLI. The secret access key is shown **once**.
* **Policies** use the familiar JSON grammar: \`Version\`, \`Statement[]\` with \`Sid\`, \`Effect\`,
  \`Action\`/\`NotAction\`, \`Resource\`/\`NotResource\` and optional \`Condition\` (\`StringEquals\`, \`StringLike\`).
  Wildcards \`*\` and \`?\` are supported.
* **Evaluation**: an explicit **Deny** wins, then any **Allow**, otherwise *implicit deny*.
* Built-in managed policies include \`AdministratorAccess\`, \`ReadOnlyAccess\`, \`CloudSimEC2FullAccess\`,
  \`CloudSimS3ReadOnlyAccess\` and more. Attach managed policies or write inline ones.
* **Roles** have a *trust policy* (who may assume them). \`sts:AssumeRole\` returns temporary credentials
  (\`ASIA…\` key + session token); the caller needs permission *and* must be trusted by the role.
* **Policy simulator**: choose a principal, an action like \`s3:GetObject\` and a resource ARN to see the decision and
  the matching statements.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim iam create-user --user-name alice
cloudsim iam create-access-key --user-name alice
cloudsim iam attach-user-policy --user-name alice --policy-name CloudSimS3ReadOnlyAccess
cloudsim sts assume-role --role-arn arn:cloudsim:iam::123456789012:role/admin --role-session-name s1
\`\`\`
`,
  },
  {
    slug: "elb",
    title: "Load balancing",
    summary: "Load balancers, listeners, target groups, health checks and traffic simulation.",
    body: `# Load balancing (simulated)

* A **load balancer** lives in one or more subnets (two or more recommended) and gets a DNS name like
  \`web-1a2b.elb.cloudsim.local\`. It moves from \`provisioning\` to \`active\`.
* **Listeners** (protocol + port) forward to a default **target group**.
* **Target groups** contain instances. Health is derived from instance state: *running* ⇒ healthy (unless a
  \`degrade\` fault is injected), not running ⇒ unused/unhealthy.
* Algorithms: \`round_robin\`, \`least_outstanding_requests\`, \`weighted_random\`.
* **Simulate traffic** sends N requests over a duration. You get the per-target distribution and latency, and the
  load is applied to the targets — their CPU rises, which can trigger alarms and auto scaling.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim elb create-target-group --name web --port 80 --vpc-id vpc-…
cloudsim elb register-targets --target-group tg-… --targets i-…
cloudsim elb simulate --load-balancer lb-… --requests 1000
\`\`\`
`,
  },
  {
    slug: "autoscaling",
    title: "Auto Scaling",
    summary: "Launch templates, groups, scaling policies and the scaling controller.",
    body: `# Auto Scaling (simulated)

* A **launch template** captures image, type, security groups, key pair, user data and workload profile.
* A **group** keeps between *min* and *max* instances, aiming for *desired*, spread over its subnets, and can
  register instances with target groups.
* **Policies**
  * *Target tracking* – keep average CPU near a target: \`desired = ceil(running × avgCPU / target)\`, scale-in only
    below 80 % of the target (hysteresis).
  * *Step* – when CPU crosses a threshold, add/remove a fixed number of instances.
* The controller evaluates every 15 s (or press **Evaluate now**). Inside the cooldown no change is made, but
  unhealthy instances are still replaced. All decisions appear in **Activity history**.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim autoscaling create-launch-template --name web --image-id ami-… --instance-type t3.micro
cloudsim autoscaling create-auto-scaling-group --name web-asg --launch-template web --min 1 --max 4 --desired 2 --subnets subnet-…
\`\`\`
`,
  },
  {
    slug: "cloudwatch",
    title: "CloudWatch – monitoring",
    summary: "Metrics, custom metrics, alarms, logs and the event history.",
    body: `# CloudWatch – monitoring (simulated)

## Metrics
Built-in metrics: \`CloudSim/EC2\` – CPUUtilization, MemoryUtilization, NetworkIn/Out, DiskRead/WriteBytes per
\`InstanceId\`; \`CloudSim/S3\` – BucketSizeBytes, NumberOfObjects; \`CloudSim/AutoScaling\` – GroupDesiredCapacity,
GroupInServiceInstances. Publish **custom metrics** to any namespace that does not start with \`CloudSim/\`.

## Alarms
An alarm watches one metric statistic over a period and compares it to a threshold for N evaluation periods.
States: \`OK\`, \`ALARM\`, \`INSUFFICIENT_DATA\`. Actions can target scaling policies or SNS topics.

## Logs
Log groups contain streams of events. The platform writes boot logs to \`/cloudsim/ec2/console\` and the audit
trail to \`/cloudsim/audit\`. Filter patterns: plain terms (all must match), \`"quoted phrase"\`, \`-term\` to exclude,
\`?a ?b\` for any-of.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim cloudwatch put-metric-data --namespace MyApp --metric-name Orders --value 3
cloudsim cloudwatch put-metric-alarm --alarm-name high-cpu --metric-name CPUUtilization --namespace CloudSim/EC2 --threshold 80 --comparison-operator GreaterThanThreshold
cloudsim logs filter-log-events --log-group-name /cloudsim/audit --filter-pattern "denied"
\`\`\`
`,
  },
  {
    slug: "messaging",
    title: "SQS & SNS – messaging",
    summary: "Queues, messages, topics, subscriptions and fan-out.",
    body: `# SQS and SNS (simulated)

## SQS
* Send messages (optional delay), **receive** up to 10 at a time with a *visibility timeout* — received messages are
  hidden from other consumers until the timeout expires — and **delete** them with their receipt handle.
* *Purge* removes all messages.

## SNS
* Topics fan out published messages to **subscriptions**: \`sqs\` (deliver into a queue) or \`log\` (write to a
  CloudWatch log group).

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim sqs create-queue --queue-name jobs
cloudsim sqs send-message --queue jobs --message-body "hello"
cloudsim sns publish --topic alerts --message "disk full"
\`\`\`
`,
  },
  {
    slug: "data",
    title: "DynamoDB, Route 53 & Secrets",
    summary: "Key-value tables, DNS zones and secret storage.",
    body: `# DynamoDB, Route 53 and Secrets Manager (simulated)

## DynamoDB
Tables have a **partition key** (\`S\` or \`N\`) and an optional **sort key**. Put items as JSON, get by key, query by
partition key with an optional sort-key prefix, or scan.

## Route 53
Hosted zones are **public** or **private** (associated with a VPC). Records: \`A\`, \`CNAME\`, \`TXT\`. Private zone
records are resolvable from inside the associated VPC (try the VPC *DNS resolver* tool).

## Secrets Manager
Secret values are redacted in listings. Reading a value (\`GetSecretValue\`) is an explicit action and is recorded in
the audit log.

## CLI
${CLI_NOTE}

\`\`\`bash
cloudsim dynamodb create-table --table-name users --partition-key id:S
cloudsim route53 create-hosted-zone --name example.internal --private --vpc-id vpc-…
cloudsim secretsmanager get-secret-value --secret-id db-password
\`\`\`
`,
  },
  {
    slug: "labs",
    title: "Training labs",
    summary: "How guided labs, validation and the in-browser terminal work.",
    body: `# Training labs

* Labs are grouped by **beginner**, **intermediate** and **advanced**. Start a lab to create a workspace; resources
  you create for it are tagged \`cloudsim:lab=<lab-id>\`.
* Each step has instructions and an optional hint. **Validate step** checks the *real simulator state* of your
  account (e.g. "a running instance tagged Name=lab-web exists") and reports the result.
* **Reset lab** deletes the lab's resources and resets your progress.
* The **terminal** next to the instructions runs the \`cloudsim\` command interpreter on the server — no shell is ever
  spawned. Useful commands: \`help\`, \`clear\`, \`whoami\`, \`echo\`, and \`profile use <iam-user>\` to act as an IAM user
  (using one of their access keys) to test permissions.
* The **Console** tab embeds the relevant console page so you can work side by side.
`,
  },
  {
    slug: "limitations",
    title: "Simulator limitations",
    summary: "What is simplified or not available in this release.",
    body: `# Simulator limitations

* **Not implemented yet:** Lambda, RDS, CloudFormation and container services (ECS/ECR/EKS). Their console pages
  explain what is planned; their API endpoints return \`501 NotImplemented\`.
* Instances never run an operating system; user data is echoed, not executed.
* Networking is evaluated by a model (documented simplification of real VPC behaviour) — no packets are sent.
* Multipart upload part-size minimums are not enforced. \`IpAddress\` policy conditions are not supported.
* Quotas and rate limits apply (default 50 requests/s per principal).
* Administrators can inject faults (instance crash, degraded health, start failure) to practise troubleshooting.
`,
  },
];

export function getDoc(slug: string): DocPage | undefined {
  return DOCS.find((d) => d.slug === slug);
}
