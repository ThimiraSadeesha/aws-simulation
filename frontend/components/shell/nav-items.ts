import {
  Activity,
  BookOpen,
  Boxes,
  Database,
  Gauge,
  GraduationCap,
  HardDrive,
  Layers,
  LayoutDashboard,
  Lock,
  Mail,
  MessagesSquare,
  Network,
  Scale,
  Server,
  Settings,
  Shield,
  Signpost,
  Table2,
  Zap,
  type LucideIcon,
} from "lucide-react";

export interface NavItem {
  label: string;
  href: string;
  icon: LucideIcon;
  /** Service not implemented in this simulator release (shows an honest placeholder). */
  unavailable?: boolean;
  group: "overview" | "services" | "learn" | "account";
}

export const NAV_ITEMS: NavItem[] = [
  { label: "Dashboard", href: "/dashboard", icon: LayoutDashboard, group: "overview" },
  { label: "EC2", href: "/ec2", icon: Server, group: "services" },
  { label: "S3", href: "/s3", icon: HardDrive, group: "services" },
  { label: "VPC", href: "/vpc", icon: Network, group: "services" },
  { label: "IAM", href: "/iam", icon: Shield, group: "services" },
  { label: "Load Balancers", href: "/elb", icon: Scale, group: "services" },
  { label: "Auto Scaling", href: "/autoscaling", icon: Gauge, group: "services" },
  { label: "CloudWatch", href: "/cloudwatch", icon: Activity, group: "services" },
  { label: "RDS", href: "/rds", icon: Database, group: "services", unavailable: true },
  { label: "Lambda", href: "/lambda", icon: Zap, group: "services", unavailable: true },
  { label: "DynamoDB", href: "/dynamodb", icon: Table2, group: "services" },
  { label: "SQS", href: "/sqs", icon: MessagesSquare, group: "services" },
  { label: "SNS", href: "/sns", icon: Mail, group: "services" },
  { label: "Route 53", href: "/route53", icon: Signpost, group: "services" },
  { label: "CloudFormation", href: "/cloudformation", icon: Layers, group: "services", unavailable: true },
  { label: "Container Services", href: "/containers", icon: Boxes, group: "services", unavailable: true },
  { label: "Secrets Manager", href: "/secrets", icon: Lock, group: "services" },
  { label: "Training Labs", href: "/labs", icon: GraduationCap, group: "learn" },
  { label: "Documentation", href: "/docs", icon: BookOpen, group: "learn" },
  { label: "Settings", href: "/settings", icon: Settings, group: "account" },
];


export function isActive(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(`${href}/`);
}
