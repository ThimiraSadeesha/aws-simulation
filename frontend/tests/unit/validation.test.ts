import { describe, expect, it } from "vitest";
import {
  validateAlarm,
  validateBucketName,
  validateCidrFormat,
  validateLaunch,
  validateRegister,
  validateSubnetCidr,
  validateVpcCidr,
  validateNamespaceForPublish,
  validatePortRange,
  type AlarmForm,
  type LaunchForm,
} from "@/lib/validation";

describe("VPC CIDR validation", () => {
  it("accepts /16–/28 aligned networks", () => {
    expect(validateVpcCidr("10.0.0.0/16")).toEqual({ error: null, warning: null });
    expect(validateVpcCidr("192.168.1.0/28").error).toBeNull();
  });
  it("rejects malformed, misaligned and out-of-range prefixes", () => {
    expect(validateVpcCidr("10.0.0.0").error).toMatch(/IPv4 CIDR/);
    expect(validateVpcCidr("10.0.0.256/16").error).toMatch(/IPv4 CIDR/);
    expect(validateVpcCidr("10.0.1.5/16").error).toMatch(/Host bits/);
    expect(validateVpcCidr("10.0.0.0/8").error).toMatch(/between \/16 and \/28/);
    expect(validateVpcCidr("10.0.0.0/29").error).toMatch(/between \/16 and \/28/);
  });
  it("warns (but allows) non-RFC1918 ranges", () => {
    const r = validateVpcCidr("100.64.0.0/16");
    expect(r.error).toBeNull();
    expect(r.warning).toMatch(/RFC 1918/);
  });
  it("validates subnet containment and overlap", () => {
    expect(validateSubnetCidr("10.0.1.0/24", "10.0.0.0/16")).toBeNull();
    expect(validateSubnetCidr("10.1.1.0/24", "10.0.0.0/16")).toMatch(/within the VPC CIDR/);
    expect(validateSubnetCidr("10.0.1.0/24", "10.0.0.0/16", [{ cidr_block: "10.0.0.0/20", subnet_id: "subnet-a" }])).toMatch(/Overlaps existing subnet subnet-a/);
    expect(validateSubnetCidr("10.0.16.0/24", "10.0.0.0/16", [{ cidr_block: "10.0.0.0/20" }])).toBeNull();
  });
  it("generic CIDRs allow 0.0.0.0/0", () => {
    expect(validateCidrFormat("0.0.0.0/0")).toBeNull();
    expect(validateCidrFormat("1.2.3.4/32")).toBeNull();
  });
});

describe("S3 bucket name validation", () => {
  it.each(["my-bucket", "logs.2026", "abc", "a".repeat(63)])("accepts %s", (n) => expect(validateBucketName(n)).toBeNull());
  it.each([
    ["ab", /3–63/],
    ["a".repeat(64), /3–63/],
    ["My-Bucket", /lowercase/],
    ["my_bucket", /lowercase/],
    ["-bucket", /begin and end/],
    ["bucket-", /begin and end/],
    ["my..bucket", /adjacent periods/],
    ["my.-bucket", /'\.-'/],
    ["192.168.1.1", /IP address/],
    ["xn--bucket", /xn--/],
  ])("rejects %s", (n, re) => expect(validateBucketName(n)).toMatch(re));
});

describe("EC2 launch validation", () => {
  const base: LaunchForm = {
    name: "web",
    image_id: "ami-1",
    instance_type: "t3.micro",
    count: 1,
    subnet_id: "",
    security_group_ids: [],
    key_name: "",
    user_data: "",
    tags: [],
  };
  it("accepts a minimal valid request", () => expect(validateLaunch(base)).toEqual({}));
  it("requires image and type", () => {
    const e = validateLaunch({ ...base, image_id: "", instance_type: "" });
    expect(e.image_id).toBeTruthy();
    expect(e.instance_type).toBeTruthy();
  });
  it("bounds the count", () => {
    expect(validateLaunch({ ...base, count: 0 }).count).toBeTruthy();
    expect(validateLaunch({ ...base, count: 21 }).count).toBeTruthy();
    expect(validateLaunch({ ...base, count: 1.5 }).count).toBeTruthy();
  });
  it("limits user data to 16 KiB", () => expect(validateLaunch({ ...base, user_data: "x".repeat(16 * 1024 + 1) }).user_data).toBeTruthy());
  it("rejects duplicate and empty tag keys", () => {
    expect(validateLaunch({ ...base, tags: [{ key: "env", value: "a" }, { key: "env", value: "b" }] }).tags).toMatch(/Duplicate/);
    expect(validateLaunch({ ...base, tags: [{ key: "", value: "b" }] }).tags).toMatch(/empty/);
  });
  it("rejects security groups from another VPC than the subnet", () => {
    const e = validateLaunch({ ...base, subnet_id: "subnet-1", security_group_ids: ["sg-a", "sg-b"] }, { subnetVpcId: "vpc-1", sgVpcIds: { "sg-a": "vpc-1", "sg-b": "vpc-2" } });
    expect(e.security_group_ids).toMatch(/sg-b/);
  });
});

describe("Alarm validation", () => {
  const ok: AlarmForm = {
    alarm_name: "high-cpu",
    namespace: "CloudSim/EC2",
    metric_name: "CPUUtilization",
    dimension_key: "InstanceId",
    dimension_value: "i-1",
    statistic: "Average",
    period: "60",
    evaluation_periods: "2",
    threshold: "80",
    comparison_operator: "GreaterThanThreshold",
    alarm_actions: "",
  };
  it("accepts a valid alarm", () => expect(validateAlarm(ok)).toEqual({}));
  it("flags each invalid field", () => {
    const e = validateAlarm({
      ...ok,
      alarm_name: " ",
      metric_name: "",
      period: "45",
      evaluation_periods: "0",
      threshold: "abc",
      comparison_operator: "Bigger",
      dimension_value: "",
      alarm_actions: "not-an-arn",
    });
    expect(Object.keys(e).sort()).toEqual(
      ["alarm_actions", "alarm_name", "comparison_operator", "dimension_value", "evaluation_periods", "metric_name", "period", "threshold"].sort(),
    );
  });
  it("custom metric namespaces must not start with CloudSim/", () => {
    expect(validateNamespaceForPublish("CloudSim/EC2")).toMatch(/CloudSim\//);
    expect(validateNamespaceForPublish("MyApp")).toBeNull();
  });
});

describe("misc validation", () => {
  it("registration rules", () => {
    expect(validateRegister({ username: "al", password: "short", confirm: "x" })).toMatchObject({
      username: expect.any(String),
      password: expect.any(String),
      confirm: expect.any(String),
    });
    expect(validateRegister({ username: "alice.dev", password: "longenough", confirm: "longenough" })).toEqual({});
  });
  it("port ranges", () => {
    expect(validatePortRange("tcp", 22, 22)).toBeNull();
    expect(validatePortRange("tcp", 80, 22)).toMatch(/less than or equal/);
    expect(validatePortRange("udp", 0, 70000)).toMatch(/0 and 65535/);
    expect(validatePortRange("-1", 0, 0)).toBeNull();
  });
});
