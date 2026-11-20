package services

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
	"cloudsim/backend/internal/simulator"
	"cloudsim/backend/internal/userdata"
)

type InstanceType struct {
	Name           string  `json:"name"`
	VCPU           int     `json:"vcpu"`
	MemoryMiB      int     `json:"memory_mib"`
	Family         string  `json:"family"`
	HourlyPriceSim float64 `json:"hourly_price_sim"`
}

// InstanceTypes is the simulated catalog (prices are illustrative only).
var InstanceTypes = []InstanceType{
	{"t3.nano", 2, 512, "t3", 0.0052}, {"t3.micro", 2, 1024, "t3", 0.0104}, {"t3.small", 2, 2048, "t3", 0.0208},
	{"t3.medium", 2, 4096, "t3", 0.0416}, {"t3.large", 2, 8192, "t3", 0.0832},
	{"m5.large", 2, 8192, "m5", 0.096}, {"m5.xlarge", 4, 16384, "m5", 0.192}, {"m5.2xlarge", 8, 32768, "m5", 0.384},
	{"c5.large", 2, 4096, "c5", 0.085}, {"c5.xlarge", 4, 8192, "c5", 0.17},
	{"r5.large", 2, 16384, "r5", 0.126},
}

type Image struct {
	ImageID      string `json:"image_id"`
	Name         string `json:"name"`
	OS           string `json:"os"`
	Description  string `json:"description"`
	Architecture string `json:"architecture"`
	BootLog      string `json:"-"`
}

// Images are simulated machine images.
var Images = []Image{
	{"ami-0c1a2b3c4d5e6f701", "cloudsim-linux-2023", "linux", "CloudSim Linux 2023 (RHEL-like, yum/dnf)", "x86_64", "CloudSim Linux 2023"},
	{"ami-0c1a2b3c4d5e6f702", "ubuntu-24.04-sim", "linux", "Ubuntu-style 24.04 LTS image (apt)", "x86_64", "Ubuntu 24.04 LTS"},
	{"ami-0c1a2b3c4d5e6f703", "debian-12-sim", "linux", "Debian-style 12 image (apt)", "x86_64", "Debian GNU/Linux 12"},
	{"ami-0c1a2b3c4d5e6f704", "cloudsim-linux-2023-arm", "linux", "CloudSim Linux 2023 for arm64", "arm64", "CloudSim Linux 2023 (arm64)"},
}

func findType(name string) (InstanceType, bool) {
	for _, t := range InstanceTypes {
		if t.Name == name {
			return t, true
		}
	}
	return InstanceType{}, false
}

func FindImage(id string) (Image, bool) {
	for _, im := range Images {
		if im.ImageID == id || im.Name == id {
			return im, true
		}
	}
	return Image{}, false
}

type EC2Service struct {
	*Deps
	Net *NetworkService
}

func NewEC2Service(d *Deps, n *NetworkService) *EC2Service { return &EC2Service{Deps: d, Net: n} }

type RunInstancesInput struct {
	ImageID           string            `json:"image_id"`
	InstanceType      string            `json:"instance_type"`
	Count             int               `json:"count"`
	Name              string            `json:"name"`
	KeyName           string            `json:"key_name"`
	SubnetID          string            `json:"subnet_id"`
	SecurityGroupIDs  []string          `json:"security_group_ids"`
	AssociatePublicIP *bool             `json:"associate_public_ip"`
	UserData          string            `json:"user_data"`
	Tags              map[string]string `json:"tags"`
	WorkloadProfile   string            `json:"workload_profile"`
	AutoScalingGroup  string            `json:"-"`
}

var workloadProfiles = map[string]bool{"idle": true, "steady": true, "spiky": true, "stress": true, "custom": true}

func (s *EC2Service) RunInstances(ctx context.Context, account string, in RunInstancesInput) ([]repositories.Instance, error) {
	img, ok := FindImage(in.ImageID)
	if !ok {
		return nil, apierr.New(400, "InvalidAMIID.NotFound", "image %q does not exist; see GET /ec2/images", in.ImageID)
	}
	it, ok := findType(in.InstanceType)
	if !ok {
		return nil, apierr.Validation("unknown instance type %q; see GET /ec2/instance-types", in.InstanceType)
	}
	if in.Count == 0 {
		in.Count = 1
	}
	if in.Count < 1 || in.Count > 20 {
		return nil, apierr.Validation("count must be between 1 and 20")
	}
	if len(in.UserData) > 16*1024 {
		return nil, apierr.Validation("user_data is limited to 16 KiB")
	}
	if in.WorkloadProfile == "" {
		in.WorkloadProfile = "steady"
	}
	if !workloadProfiles[in.WorkloadProfile] {
		return nil, apierr.Validation("workload_profile must be idle, steady, spiky, stress or custom")
	}
	if err := ValidateTags(in.Tags); err != nil {
		return nil, err
	}
	if in.KeyName != "" {
		if _, err := s.Store.EC2.GetKeyPair(ctx, account, in.KeyName); err != nil {
			return nil, apierr.New(400, "InvalidKeyPair.NotFound", "key pair %q does not exist", in.KeyName)
		}
	}
	var subnet *repositories.Subnet
	var err error
	if in.SubnetID == "" {
		if _, subnet, err = s.Net.EnsureDefaultVPC(ctx, account); err != nil {
			return nil, err
		}
	} else if subnet, err = s.Net.GetSubnet(ctx, account, in.SubnetID); err != nil {
		return nil, err
	}
	groups, err := s.Net.ResolveSGs(ctx, account, subnet.VPCID, in.SecurityGroupIDs)
	if err != nil {
		return nil, err
	}
	assignPublic := subnet.MapPublicIPOnLaunch
	if in.AssociatePublicIP != nil {
		assignPublic = *in.AssociatePublicIP
	}
	boot := userdata.Run(in.UserData)
	bootJSON, _ := json.Marshal(boot)
	pfx := netip.MustParsePrefix(subnet.CIDRBlock)
	var created []repositories.Instance
	for n := 0; n < in.Count; n++ {
		inst := repositories.Instance{
			InstanceID: ids.New("i"), AccountID: account, InstanceType: it.Name, VCPU: it.VCPU, MemoryMiB: it.MemoryMiB,
			ImageID: img.ImageID, KeyName: in.KeyName, VPCID: subnet.VPCID, SubnetID: subnet.SubnetID, State: "pending",
			UserData: in.UserData, BootResult: string(bootJSON), WorkloadProfile: in.WorkloadProfile, AutoScalingGroup: in.AutoScalingGroup,
		}
		if assignPublic {
			seq, err := s.NextSeq(ctx, "public_ip")
			if err != nil {
				return created, err
			}
			inst.PublicIP = PublicIP(seq)
		}
		tags := withName(in.Tags, in.Name)
		if in.Name != "" && in.Count > 1 {
			tags["Name"] = fmt.Sprintf("%s-%d", in.Name, n+1)
		}
		err := s.Store.DB.Tx(ctx, func(tx *sql.Tx) error {
			used, err := s.Store.Network.UsedIPsQ(ctx, tx, subnet.SubnetID)
			if err != nil {
				return err
			}
			if inst.PrivateIP, err = AllocateIP(pfx, used); err != nil {
				return err
			}
			if err := s.Store.EC2.InsertInstance(ctx, tx, &inst); err != nil {
				return err
			}
			eni := &repositories.NetworkInterface{NetworkInterfaceID: ids.New("eni"), AccountID: account, SubnetID: subnet.SubnetID, VPCID: subnet.VPCID,
				PrivateIP: inst.PrivateIP, PublicIP: inst.PublicIP, Description: "Primary network interface", SecurityGroupIDs: groups,
				Attachment: &repositories.ENIAttachment{InstanceID: inst.InstanceID}}
			if err := s.Store.Network.InsertENI(ctx, tx, eni); err != nil {
				return err
			}
			return s.Store.Tags.Replace(ctx, tx, account, inst.InstanceID, tags)
		})
		if err != nil {
			return created, err
		}
		res, err := s.Engine.Register(ctx, inst.InstanceID, simulator.RegisterRequest{
			Kind: "instance", Owner: account, DependsOn: []string{subnet.SubnetID},
			Attributes: map[string]any{"instance_type": it.Name, "vcpu": it.VCPU, "memory_mib": it.MemoryMiB, "workload": in.WorkloadProfile},
		})
		if err != nil {
			s.purgeInstance(ctx, inst.InstanceID)
			return created, EngineErr(err)
		}
		if res.State != "" && res.State != inst.State {
			_ = s.Store.EC2.UpdateState(ctx, inst.InstanceID, res.State, "")
		}
		s.RecordEvent(ctx, account, inst.InstanceID, "instance", "launched", fmt.Sprintf("Instance %s launched (%s, %s) in %s", inst.InstanceID, it.Name, img.Name, subnet.SubnetID), nil)
		s.Logs.Append(ctx, account, "/cloudsim/ec2/console", inst.InstanceID, s.bootLines(&inst, img, boot))
		got, err := s.Store.EC2.GetInstance(ctx, account, inst.InstanceID)
		if err != nil {
			return created, err
		}
		got.Tags = tags
		created = append(created, *got)
	}
	return created, nil
}

func (s *EC2Service) purgeInstance(ctx context.Context, id string) {
	_ = s.Store.Network.DetachENIs(ctx, nil, id)
	_ = s.Store.Tags.Delete(ctx, nil, id)
	_, _ = s.Store.DB.ExecContext(ctx, `DELETE FROM instances WHERE instance_id=?`, id)
}

func (s *EC2Service) Get(ctx context.Context, account, id string) (*repositories.Instance, error) {
	i, err := s.Store.EC2.GetInstance(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "instance "+id)
	}
	i.Tags, _ = s.Store.Tags.Get(ctx, id)
	return i, nil
}

func (s *EC2Service) List(ctx context.Context, account string, f repositories.InstanceFilter) ([]repositories.Instance, error) {
	f.AccountID = account
	l, err := s.Store.EC2.ListInstances(ctx, f)
	if err != nil {
		return nil, err
	}
	return l, s.Net.attachTags(ctx, len(l), func(i int) (string, *map[string]string) { return l[i].InstanceID, &l[i].Tags })
}

var actionNames = map[string]string{"start": "start", "stop": "stop", "reboot": "reboot", "terminate": "terminate"}

// Action applies a lifecycle action via the engine's state machine.
func (s *EC2Service) Action(ctx context.Context, account, id, action, reason string) (*repositories.Instance, error) {
	if _, ok := actionNames[action]; !ok {
		return nil, apierr.Validation("unknown action %q", action)
	}
	inst, err := s.Get(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if inst.State == "terminated" {
		return nil, apierr.InvalidState("instance %s is terminated", id)
	}
	if reason == "" {
		reason = "User initiated (" + time.Now().UTC().Format(time.RFC3339) + ")"
	}
	res, err := s.Engine.Action(ctx, id, action, reason)
	if err != nil {
		return nil, EngineErr(err)
	}
	if err := s.Store.EC2.UpdateState(ctx, id, res.State, reason); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, id, "instance", "action", fmt.Sprintf("%s requested for %s: %s → %s", action, id, inst.State, res.State), map[string]string{"action": action})
	if res.State == "terminated" {
		s.onTerminated(ctx, account, id)
	}
	return s.Get(ctx, account, id)
}

// OnInstanceState applies an engine state change to the database (called by the event sync).
func (s *EC2Service) OnInstanceState(ctx context.Context, id, from, to, reason string) {
	inst, err := s.Store.EC2.GetInstanceAny(ctx, id)
	if err != nil {
		return
	}
	if inst.State == "terminated" {
		return
	}
	if err := s.Store.EC2.UpdateState(ctx, id, to, reason); err != nil {
		s.Log.Error("update instance state", "id", id, "err", err)
		return
	}
	s.RecordEvent(ctx, inst.AccountID, id, "instance", "state_changed", fmt.Sprintf("%s: %s → %s", id, from, to), map[string]string{"from": from, "to": to, "reason": reason})
	if to == "running" && from == "pending" {
		img, _ := FindImage(inst.ImageID)
		s.Logs.Append(ctx, inst.AccountID, "/cloudsim/ec2/console", id, []string{fmt.Sprintf("[%s] %s login: (simulated console) instance is running", time.Now().UTC().Format(time.RFC3339), img.Name)})
	}
	if to == "terminated" {
		s.onTerminated(ctx, inst.AccountID, id)
	}
}

func (s *EC2Service) onTerminated(ctx context.Context, account, id string) {
	// Release the network interface and public address, and drop the engine registration.
	_ = s.Store.Network.DetachENIs(ctx, nil, id)
	_ = s.Store.EC2.SetPublicIP(ctx, id, "")
	_ = s.Engine.Deregister(ctx, id)
	for _, h := range TerminationHooks {
		h(ctx, account, id)
	}
}

// TerminationHooks run after an instance terminates (e.g. ELB target deregistration, ASG replacement).
var TerminationHooks []func(ctx context.Context, account, instanceID string)

func (s *EC2Service) SetTags(ctx context.Context, account, id string, tags map[string]string) (*repositories.Instance, error) {
	if _, err := s.Get(ctx, account, id); err != nil {
		return nil, err
	}
	if err := ValidateTags(tags); err != nil {
		return nil, err
	}
	if err := s.Store.Tags.Replace(ctx, nil, account, id, tags); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, id, "instance", "tags_updated", "Tags updated on "+id, tags)
	return s.Get(ctx, account, id)
}

type WorkloadInput struct {
	Profile   string   `json:"profile"`
	CPUTarget *float64 `json:"cpu_target"`
}

func (s *EC2Service) SetWorkload(ctx context.Context, account, id string, in WorkloadInput) (*repositories.Instance, error) {
	if !workloadProfiles[in.Profile] {
		return nil, apierr.Validation("profile must be idle, steady, spiky, stress or custom")
	}
	if in.Profile == "custom" && (in.CPUTarget == nil || *in.CPUTarget < 0 || *in.CPUTarget > 100) {
		return nil, apierr.Validation("custom profile requires cpu_target between 0 and 100")
	}
	if in.Profile != "custom" {
		in.CPUTarget = nil
	}
	inst, err := s.Get(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if inst.State == "terminated" {
		return nil, apierr.InvalidState("instance is terminated")
	}
	if err := s.Engine.SetWorkload(ctx, id, simulator.Workload{Profile: in.Profile, CPUTarget: in.CPUTarget}); err != nil {
		return nil, EngineErr(err)
	}
	if err := s.Store.EC2.UpdateWorkload(ctx, id, in.Profile, in.CPUTarget); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, id, "instance", "workload_changed", "Workload of "+id+" set to "+in.Profile, in)
	return s.Get(ctx, account, id)
}

func (s *EC2Service) SetSecurityGroups(ctx context.Context, account, id string, groups []string) (*repositories.Instance, error) {
	inst, err := s.Get(ctx, account, id)
	if err != nil {
		return nil, err
	}
	if inst.State == "terminated" {
		return nil, apierr.InvalidState("instance is terminated")
	}
	if len(groups) == 0 {
		return nil, apierr.Validation("at least one security group is required")
	}
	resolved, err := s.Net.ResolveSGs(ctx, account, inst.VPCID, groups)
	if err != nil {
		return nil, err
	}
	eni, err := s.Store.Network.PrimaryENI(ctx, id)
	if err != nil {
		return nil, RepoErr(err, "primary network interface")
	}
	if err := s.Store.Network.SetENISecurityGroups(ctx, nil, eni.NetworkInterfaceID, resolved); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, id, "instance", "security_groups_changed", "Security groups of "+id+" set to "+strings.Join(resolved, ", "), nil)
	return s.Get(ctx, account, id)
}

// Boot returns the parsed user-data result for an instance.
func (s *EC2Service) Boot(inst *repositories.Instance) userdata.Result {
	var r userdata.Result
	_ = json.Unmarshal([]byte(inst.BootResult), &r)
	if r.Services == nil {
		r.Services = map[string]int{"sshd": 22}
	}
	return r
}

// ListeningPorts returns ports with a running service when the instance is running.
func (s *EC2Service) ListeningPorts(inst *repositories.Instance) map[int]string {
	out := map[int]string{}
	if inst.State != "running" {
		return out
	}
	for svc, port := range s.Boot(inst).Services {
		if port > 0 {
			out[port] = svc
		}
	}
	return out
}

func (s *EC2Service) bootLines(inst *repositories.Instance, img Image, boot userdata.Result) []string {
	ts := time.Now().UTC().Format(time.RFC3339)
	lines := []string{
		fmt.Sprintf("[%s] CloudSim simulated boot — this is not a real virtual machine", ts),
		fmt.Sprintf("[    0.000000] Linux version 6.1.0-cloudsim (%s)", img.BootLog),
		fmt.Sprintf("[    0.000000] Command line: BOOT_IMAGE=/vmlinuz root=/dev/xvda1 console=ttyS0"),
		fmt.Sprintf("[    0.104211] CPU: %d vCPU(s), Memory: %d MiB (%s)", inst.VCPU, inst.MemoryMiB, inst.InstanceType),
		fmt.Sprintf("[    1.220031] eth0: link up, address %s", inst.PrivateIP),
		"[    2.004410] cloud-init[812]: Cloud-init v. 23.4 (simulated) running 'init'",
		fmt.Sprintf("[    2.105510] cloud-init[812]: hostname set to %s", strings.TrimSuffix(inst.PrivateDNS, ".cloudsim.internal")),
	}
	if inst.KeyName != "" {
		lines = append(lines, "[    2.211002] cloud-init[812]: authorized key installed for ec2-user from key pair '"+inst.KeyName+"'")
	}
	if boot.Ran {
		lines = append(lines, "[    3.000100] cloud-init[812]: running user-data in CloudSim sandbox (interpreted, not executed on a host)")
		for _, l := range boot.Output {
			lines = append(lines, "[    3.0] user-data: "+l)
		}
		lines = append(lines, fmt.Sprintf("[    3.900000] cloud-init[812]: user-data finished with exit code %d", boot.ExitCode))
	}
	return lines
}

// ConsoleOutput reconstructs the instance's serial console.
func (s *EC2Service) ConsoleOutput(ctx context.Context, account, id string) (map[string]any, error) {
	inst, err := s.Get(ctx, account, id)
	if err != nil {
		return nil, err
	}
	img, _ := FindImage(inst.ImageID)
	lines := s.bootLines(inst, img, s.Boot(inst))
	evs, _, err := s.Store.Events.List(ctx, repositories.EventFilter{AccountID: account, ResourceID: id, Limit: 200})
	if err == nil {
		for k := len(evs) - 1; k >= 0; k-- {
			lines = append(lines, fmt.Sprintf("[%s] cloudsim: %s", evs[k].TS, evs[k].Message))
		}
	}
	return map[string]any{"instance_id": id, "output": strings.Join(lines, "\n") + "\n", "ts": time.Now().UTC().Format(time.RFC3339)}, nil
}

// Metadata returns an IMDS-style view.
func (s *EC2Service) Metadata(ctx context.Context, account, id string) (map[string]any, error) {
	inst, err := s.Get(ctx, account, id)
	if err != nil {
		return nil, err
	}
	sn, _ := s.Store.Network.GetSubnet(ctx, account, inst.SubnetID)
	az := "local-1a"
	if sn != nil {
		az = sn.AvailabilityZone
	}
	names := []string{}
	for _, g := range inst.SecurityGroupIDs {
		if sg, err := s.Store.Network.GetSG(ctx, account, g); err == nil {
			names = append(names, sg.GroupName)
		}
	}
	boot := s.Boot(inst)
	m := map[string]any{
		"ami-id": inst.ImageID, "instance-id": inst.InstanceID, "instance-type": inst.InstanceType,
		"local-ipv4": inst.PrivateIP, "local-hostname": inst.PrivateDNS, "public-ipv4": inst.PublicIP,
		"placement/availability-zone": az, "placement/region": "local-1", "security-groups": strings.Join(names, "\n"),
		"network/interfaces/macs/0/subnet-id": inst.SubnetID, "network/interfaces/macs/0/vpc-id": inst.VPCID,
		"services/running": boot.Services, "packages/installed": boot.Packages, "user-data": inst.UserData,
	}
	for k, v := range inst.Tags {
		m["tags/instance/"+k] = v
	}
	if inst.KeyName != "" {
		m["public-keys/0/openssh-key"] = inst.KeyName
	}
	return m, nil
}

func (s *EC2Service) History(ctx context.Context, account, id string) ([]repositories.Event, error) {
	if _, err := s.Get(ctx, account, id); err != nil {
		return nil, err
	}
	evs, _, err := s.Store.Events.List(ctx, repositories.EventFilter{AccountID: account, ResourceID: id, Limit: 200})
	return evs, err
}

// ---- Key pairs

var keyNameRe = regexp.MustCompile(`^[A-Za-z0-9 ._\-]{1,255}$`)

type CreatedKeyPair struct {
	repositories.KeyPair
	PrivateKeyPEM string `json:"private_key_pem"`
}

// CreateKeyPair generates an ed25519 key; the private key is returned once and never stored.
func (s *EC2Service) CreateKeyPair(ctx context.Context, account, name string) (*CreatedKeyPair, error) {
	if !keyNameRe.MatchString(name) {
		return nil, apierr.Validation("key pair name must be 1-255 characters of letters, digits, spaces, '.', '_' or '-'")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(priv, name)
	if err != nil {
		return nil, err
	}
	k := repositories.KeyPair{KeyPairID: ids.New("key"), AccountID: account, Name: name,
		Fingerprint: ssh.FingerprintSHA256(sshPub), PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))}
	if err := s.Store.EC2.InsertKeyPair(ctx, &k); err != nil {
		return nil, RepoErr(err, "key pair "+name)
	}
	s.RecordEvent(ctx, account, k.KeyPairID, "key_pair", "created", "Key pair "+name+" created", nil)
	return &CreatedKeyPair{KeyPair: k, PrivateKeyPEM: string(pem.EncodeToMemory(block))}, nil
}

func (s *EC2Service) DeleteKeyPair(ctx context.Context, account, name string) error {
	if err := s.Store.EC2.DeleteKeyPair(ctx, account, name); err != nil {
		return RepoErr(err, "key pair "+name)
	}
	s.RecordEvent(ctx, account, name, "key_pair", "deleted", "Key pair "+name+" deleted", nil)
	return nil
}
