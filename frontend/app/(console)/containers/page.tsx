import { NotAvailable } from "@/components/console/not-available";

export const metadata = { title: "Container Services" };

export default function Page() {
  return (
    <NotAvailable
      service="Container Services"
      summary="ECS, ECR and EKS style container orchestration."
      planned={[
        "ECR-style image repositories (metadata only – no images are pulled or run).",
        "ECS-style clusters, task definitions and services scheduled onto simulated instances.",
        "EKS-style clusters with node groups backed by Auto Scaling groups.",
      ]}
    />
  );
}
