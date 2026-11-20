export type PrincipalKind = "users" | "groups" | "roles";

export interface CallerIdentity {
  account: string;
  arn: string;
  user_id: string;
}

export interface AssumeRoleResponse {
  credentials: {
    access_key_id: string;
    secret_access_key: string;
    session_token: string;
    expiration: string;
  };
  assumed_role_user: { arn: string };
}

export const DEFAULT_POLICY = {
  Version: "2012-10-17",
  Statement: [
    {
      Sid: "AllowDescribeInstances",
      Effect: "Allow",
      Action: ["ec2:DescribeInstances"],
      Resource: "*",
    },
  ],
};

export function defaultTrustPolicy(accountId?: string) {
  return {
    Version: "2012-10-17",
    Statement: [
      {
        Sid: "AllowAccountToAssume",
        Effect: "Allow",
        Principal: { CloudSim: `arn:cloudsim:iam::${accountId || "<account_id>"}:root` },
        Action: "sts:AssumeRole",
      },
    ],
  };
}

export const COMMON_ACTIONS = [
  "ec2:DescribeInstances",
  "ec2:RunInstances",
  "ec2:StartInstances",
  "ec2:StopInstances",
  "ec2:TerminateInstances",
  "ec2:CreateTags",
  "ec2:DescribeImages",
  "ec2:CreateKeyPair",
  "s3:ListAllMyBuckets",
  "s3:CreateBucket",
  "s3:DeleteBucket",
  "s3:ListBucket",
  "s3:GetObject",
  "s3:PutObject",
  "s3:DeleteObject",
  "s3:PutBucketPolicy",
  "iam:ListUsers",
  "iam:CreateUser",
  "iam:GetUser",
  "iam:DeleteUser",
  "iam:AttachUserPolicy",
  "sts:AssumeRole",
  "cloudwatch:GetMetricData",
  "cloudwatch:PutMetricAlarm",
  "logs:FilterLogEvents",
  "sqs:SendMessage",
  "sqs:ReceiveMessage",
  "sns:Publish",
  "secretsmanager:GetSecretValue",
  "dynamodb:PutItem",
  "dynamodb:Query",
];
