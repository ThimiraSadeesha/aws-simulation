import { NotAvailable } from "@/components/console/not-available";

export const metadata = { title: "Lambda" };

export default function Page() {
  return (
    <NotAvailable
      service="Lambda"
      summary="Serverless functions."
      planned={[
        "Function definitions with runtime, memory, timeout and an execution role checked by the IAM policy engine.",
        "Simulated invocations with logs written to CloudWatch Logs (no user code is executed by the simulator).",
        "Triggers from SQS queues and SNS topics.",
      ]}
    />
  );
}
