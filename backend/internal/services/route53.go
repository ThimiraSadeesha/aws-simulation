package services

import (
	"context"
	"net/netip"
	"regexp"
	"strings"

	"cloudsim/backend/internal/apierr"
	"cloudsim/backend/internal/db"
	"cloudsim/backend/internal/ids"
	"cloudsim/backend/internal/repositories"
)

const r53MaxTTL = 604800

var dnsLabelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

// Route53Service implements hosted zones and record sets, answering the VPC resolver.
type Route53Service struct {
	*Deps
	Repo repositories.Route53Repo
	Net  *NetworkService
}

func NewRoute53Service(d *Deps, n *NetworkService) *Route53Service {
	return &Route53Service{Deps: d, Repo: repositories.NewRoute53Repo(d.Store.DB), Net: n}
}

func ZoneARN(id string) string { return "arn:cloudsim:route53:::hostedzone/" + id }

// normDomain lowercases, strips the trailing dot and validates a DNS name.
func normDomain(name string, allowWildcard bool) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if n == "" || len(n) > 253 {
		return "", apierr.Validation("domain name must be 1-253 characters")
	}
	for i, l := range strings.Split(n, ".") {
		if allowWildcard && i == 0 && l == "*" {
			continue
		}
		if !dnsLabelRe.MatchString(l) {
			return "", apierr.Validation("invalid domain name %q: labels must be 1-63 chars of letters, digits, '-' or '_' and not start/end with '-'", name)
		}
	}
	return n, nil
}

type ZoneView struct {
	repositories.HostedZone
	ARN         string   `json:"arn"`
	NameServers []string `json:"name_servers"`
}

func nameServers(id string) []string {
	suffix := strings.ToLower(id[len(id)-4:])
	return []string{"ns-1-" + suffix + ".awsdns.cloudsim.local", "ns-2-" + suffix + ".awsdns.cloudsim.local",
		"ns-3-" + suffix + ".awsdns.cloudsim.local", "ns-4-" + suffix + ".awsdns.cloudsim.local"}
}

func zoneView(z *repositories.HostedZone) *ZoneView {
	return &ZoneView{HostedZone: *z, ARN: ZoneARN(z.ZoneID), NameServers: nameServers(z.ZoneID)}
}

type CreateZoneInput struct {
	Name    string `json:"name"`
	Private bool   `json:"private"`
	VPCID   string `json:"vpc_id"`
	Comment string `json:"comment"`
}

func (s *Route53Service) CreateZone(ctx context.Context, account string, in CreateZoneInput) (*ZoneView, error) {
	name, err := normDomain(in.Name, false)
	if err != nil {
		return nil, err
	}
	if !in.Private && !strings.Contains(name, ".") {
		return nil, apierr.Validation("public hosted zones need at least two labels (e.g. example.com)")
	}
	if len(in.Comment) > 256 {
		return nil, apierr.Validation("comment must be at most 256 characters")
	}
	if in.Private {
		if in.VPCID == "" {
			return nil, apierr.Validation("vpc_id is required for a private hosted zone")
		}
		if _, err := s.Net.GetVPC(ctx, account, in.VPCID); err != nil {
			return nil, err
		}
	} else if in.VPCID != "" {
		return nil, apierr.Validation("vpc_id is only valid for private hosted zones")
	}
	existing, err := s.Repo.ListZones(ctx, account)
	if err != nil {
		return nil, err
	}
	for _, z := range existing {
		if z.Name == name && z.Private == in.Private && z.VPCID == in.VPCID {
			return nil, apierr.Conflict("a hosted zone %s already exists (%s)", name, z.ZoneID)
		}
	}
	now := db.Now()
	z := &repositories.HostedZone{ZoneID: "Z" + strings.ToUpper(ids.New("x")[2:]), AccountID: account, Name: name, Private: in.Private,
		VPCID: in.VPCID, Comment: in.Comment, CreatedAt: now}
	ns := nameServers(z.ZoneID)
	defaults := []repositories.RecordSet{
		{ZoneID: z.ZoneID, Name: name, Type: "NS", TTL: 172800, Values: ns, UpdatedAt: now},
		{ZoneID: z.ZoneID, Name: name, Type: "SOA", TTL: 900, Values: []string{ns[0] + " hostmaster.cloudsim.local 1 7200 900 1209600 86400"}, UpdatedAt: now},
	}
	if err := s.registerSimple(ctx, z.ZoneID, "hosted_zone", account, nil, map[string]any{"name": name, "private": in.Private}); err != nil {
		return nil, err
	}
	if err := s.Repo.InsertZone(ctx, z, defaults); err != nil {
		_ = s.Engine.Deregister(ctx, z.ZoneID)
		return nil, RepoErr(err, "hosted zone "+name)
	}
	z.RecordCount = len(defaults)
	s.RecordEvent(ctx, account, z.ZoneID, "hosted_zone", "created", "Hosted zone "+name+" created", map[string]any{"private": in.Private, "vpc_id": in.VPCID})
	return zoneView(z), nil
}

func (s *Route53Service) GetZone(ctx context.Context, account, id string) (*ZoneView, error) {
	z, err := s.Repo.GetZone(ctx, account, id)
	if err != nil {
		return nil, RepoErr(err, "hosted zone "+id)
	}
	return zoneView(z), nil
}

func (s *Route53Service) ListZones(ctx context.Context, account string) ([]ZoneView, error) {
	l, err := s.Repo.ListZones(ctx, account)
	if err != nil {
		return nil, err
	}
	out := make([]ZoneView, 0, len(l))
	for i := range l {
		out = append(out, *zoneView(&l[i]))
	}
	return out, nil
}

func (s *Route53Service) DeleteZone(ctx context.Context, account, id string) error {
	z, err := s.GetZone(ctx, account, id)
	if err != nil {
		return err
	}
	recs, err := s.Repo.ListRecords(ctx, id)
	if err != nil {
		return err
	}
	for _, r := range recs {
		if r.Type != "NS" && r.Type != "SOA" {
			return apierr.New(409, "HostedZoneNotEmpty", "hosted zone %s still contains record sets other than the default NS and SOA (e.g. %s %s)", z.Name, r.Name, r.Type)
		}
	}
	if err := s.deregister(ctx, id); err != nil {
		return err
	}
	if err := s.Repo.DeleteZone(ctx, id); err != nil {
		return RepoErr(err, "hosted zone "+id)
	}
	s.RecordEvent(ctx, account, id, "hosted_zone", "deleted", "Hosted zone "+z.Name+" deleted", nil)
	return nil
}

func (s *Route53Service) ListRecords(ctx context.Context, account, zoneID string) ([]repositories.RecordSet, error) {
	if _, err := s.GetZone(ctx, account, zoneID); err != nil {
		return nil, err
	}
	return s.Repo.ListRecords(ctx, zoneID)
}

type RecordInput struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	TTL    *int     `json:"ttl"`
	Values []string `json:"values"`
}

// recordName resolves "@" / relative / absolute names and checks the name is inside the zone.
func recordName(zone, name string) (string, error) {
	if strings.TrimSpace(name) == "@" {
		return zone, nil
	}
	n, err := normDomain(name, true)
	if err != nil {
		return "", err
	}
	if n != zone && !strings.HasSuffix(n, "."+zone) {
		return "", apierr.Validation("record name %s is not inside hosted zone %s", n, zone)
	}
	return n, nil
}

// UpsertRecord creates or replaces a record set (ChangeResourceRecordSets UPSERT).
func (s *Route53Service) UpsertRecord(ctx context.Context, account, zoneID string, in RecordInput) (*repositories.RecordSet, error) {
	z, err := s.GetZone(ctx, account, zoneID)
	if err != nil {
		return nil, err
	}
	name, err := recordName(z.Name, in.Name)
	if err != nil {
		return nil, err
	}
	typ := strings.ToUpper(strings.TrimSpace(in.Type))
	ttl := 300
	if in.TTL != nil {
		ttl = *in.TTL
	}
	if ttl < 0 || ttl > r53MaxTTL {
		return nil, apierr.Validation("ttl must be between 0 and %d seconds", r53MaxTTL)
	}
	if len(in.Values) == 0 || len(in.Values) > 100 {
		return nil, apierr.Validation("values must contain 1-100 entries")
	}
	vals := make([]string, 0, len(in.Values))
	seen := map[string]bool{}
	for _, v := range in.Values {
		v = strings.TrimSpace(v)
		switch typ {
		case "A":
			a, err := netip.ParseAddr(v)
			if err != nil || !a.Is4() {
				return nil, apierr.Validation("A record value %q is not an IPv4 address", v)
			}
			v = a.String()
		case "AAAA":
			a, err := netip.ParseAddr(v)
			if err != nil || !a.Is6() || a.Is4In6() {
				return nil, apierr.Validation("AAAA record value %q is not an IPv6 address", v)
			}
			v = a.String()
		case "CNAME":
			if len(in.Values) != 1 {
				return nil, apierr.Validation("a CNAME record must have exactly one value")
			}
			if v, err = normDomain(v, false); err != nil {
				return nil, err
			}
			if v == name {
				return nil, apierr.Validation("a CNAME cannot point to itself")
			}
		case "TXT":
			if v == "" || len(v) > 255 {
				return nil, apierr.Validation("each TXT value must be 1-255 characters")
			}
		case "NS", "SOA":
			return nil, apierr.Validation("NS and SOA records are managed by CloudSim and cannot be changed")
		default:
			return nil, apierr.Validation("type must be one of A, AAAA, CNAME, TXT")
		}
		if !seen[v] {
			seen[v] = true
			vals = append(vals, v)
		}
	}
	existing, err := s.Repo.RecordsByName(ctx, zoneID, name)
	if err != nil {
		return nil, err
	}
	if typ == "CNAME" {
		if name == z.Name {
			return nil, apierr.Validation("a CNAME record is not allowed at the zone apex")
		}
		for _, r := range existing {
			if r.Type != "CNAME" {
				return nil, apierr.Conflict("cannot create CNAME %s: a %s record already exists at that name", name, r.Type)
			}
		}
	} else {
		for _, r := range existing {
			if r.Type == "CNAME" {
				return nil, apierr.Conflict("cannot create %s %s: a CNAME record already exists at that name", typ, name)
			}
		}
	}
	rs := &repositories.RecordSet{ZoneID: zoneID, Name: name, Type: typ, TTL: ttl, Values: vals, UpdatedAt: db.Now()}
	if err := s.Repo.UpsertRecord(ctx, rs); err != nil {
		return nil, err
	}
	s.RecordEvent(ctx, account, zoneID, "hosted_zone", "record_upserted", "Record "+name+" "+typ+" upserted in "+z.Name, map[string]any{"values": vals, "ttl": ttl})
	return rs, nil
}

func (s *Route53Service) DeleteRecord(ctx context.Context, account, zoneID, name, typ string) error {
	z, err := s.GetZone(ctx, account, zoneID)
	if err != nil {
		return err
	}
	n, err := recordName(z.Name, name)
	if err != nil {
		return err
	}
	typ = strings.ToUpper(typ)
	if typ == "NS" || typ == "SOA" {
		return apierr.Validation("the default NS and SOA records cannot be deleted")
	}
	ok, err := s.Repo.DeleteRecord(ctx, zoneID, n, typ)
	if err != nil {
		return err
	}
	if !ok {
		return apierr.NotFound("record %s %s not found in %s", n, typ, z.Name)
	}
	s.RecordEvent(ctx, account, zoneID, "hosted_zone", "record_deleted", "Record "+n+" "+typ+" deleted from "+z.Name, nil)
	return nil
}

// bestZone returns the most specific zone visible from vpcID containing name (private zones win ties).
func bestZone(zones []repositories.HostedZone, name, vpcID string) *repositories.HostedZone {
	var best *repositories.HostedZone
	for i := range zones {
		z := &zones[i]
		if z.Private && (vpcID == "" || z.VPCID != vpcID) {
			continue
		}
		if name != z.Name && !strings.HasSuffix(name, "."+z.Name) {
			continue
		}
		if best == nil || len(z.Name) > len(best.Name) || (len(z.Name) == len(best.Name) && z.Private && !best.Private) {
			best = z
		}
	}
	return best
}

func answers(recs []repositories.RecordSet, types ...string) []map[string]any {
	out := []map[string]any{}
	for _, t := range types {
		for _, r := range recs {
			if r.Type != t {
				continue
			}
			for _, v := range r.Values {
				out = append(out, map[string]any{"type": r.Type, "value": v, "ttl": r.TTL})
			}
		}
	}
	return out
}

// Resolve answers a DNS query from the account's hosted zones. Private zones only answer for their VPC.
// A CNAME is followed one hop within the account's zones.
func (s *Route53Service) Resolve(ctx context.Context, account, name, vpcID string) ([]map[string]any, string) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	zones, err := s.Repo.ListZones(ctx, account)
	if err != nil || len(zones) == 0 {
		return nil, ""
	}
	lookup := func(n string) []repositories.RecordSet {
		z := bestZone(zones, n, vpcID)
		if z == nil {
			return nil
		}
		recs, err := s.Repo.RecordsByName(ctx, z.ZoneID, n)
		if err != nil {
			return nil
		}
		return recs
	}
	recs := lookup(name)
	out := answers(recs, "CNAME")
	if len(out) > 0 {
		target, _ := out[0]["value"].(string)
		out = append(out, answers(lookup(target), "A", "AAAA")...)
	} else {
		out = answers(recs, "A", "AAAA", "TXT")
	}
	if len(out) == 0 {
		return nil, ""
	}
	return out, "route53"
}

func (s *Route53Service) Resync(ctx context.Context) error {
	zs, err := s.Repo.ListAllZones(ctx)
	if err != nil {
		return err
	}
	for _, z := range zs {
		if err := s.resyncSimple(ctx, z.ZoneID, "hosted_zone", z.AccountID, nil); err != nil {
			return err
		}
	}
	return nil
}
