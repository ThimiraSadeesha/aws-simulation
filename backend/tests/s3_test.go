package tests

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"cloudsim/backend/internal/testutil"
)

const s3p = "/api/v1/s3/buckets"

func objPath(bucket, key string) string {
	return s3p + "/" + bucket + "/objects/" + url.PathEscape(key)
}

func mkBucket(t *testing.T, c *testutil.Client, name string) {
	t.Helper()
	c.Must(201, "POST", s3p, map[string]any{"name": name})
}

func put(t *testing.T, c *testutil.Client, bucket, key string, body []byte, headers ...string) map[string]any {
	t.Helper()
	r := c.Do("PUT", objPath(bucket, key), body, headers...)
	if r.Status != 200 {
		t.Fatalf("put %s/%s: %d %s", bucket, key, r.Status, r.Body)
	}
	return r.Map()
}

func blobFiles(t *testing.T, h *testutil.Harness, bucket string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(h.Cfg.ObjectStorageDir, bucket))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func TestS3BucketCRUDAndValidation(t *testing.T) {
	h := testutil.New(t)
	alice, bob := h.Signup("alice"), h.Signup("bob")
	b := alice.Must(201, "POST", s3p, map[string]any{"name": "alice-data"})
	if b["region"] != "local-1" || b["versioning"] != "Disabled" || b["acl"] != "private" || b["has_policy"] != false {
		t.Fatalf("bucket: %v", b)
	}
	if r := alice.Do("POST", s3p, map[string]any{"name": "alice-data"}); r.Status != 409 || r.ErrCode() != "BucketAlreadyOwnedByYou" {
		t.Fatalf("dup own: %d %s", r.Status, r.Body)
	}
	if r := bob.Do("POST", s3p, map[string]any{"name": "alice-data"}); r.Status != 409 || r.ErrCode() != "BucketAlreadyExists" {
		t.Fatalf("dup global: %d %s", r.Status, r.Body)
	}
	if r := bob.Do("GET", s3p+"/alice-data", nil); r.Status != 404 {
		t.Fatalf("other account must see 404: %d", r.Status)
	}
	if r := bob.Do("GET", s3p+"/alice-data/objects", nil); r.Status != 404 {
		t.Fatalf("other account list: %d", r.Status)
	}
	for _, n := range []string{"ab", strings.Repeat("a", 64), "UPPER", "192.168.1.1", "a..b", "-abc", "abc-", "a_b", "xn--abc", "my.-bucket"} {
		if r := alice.Do("POST", s3p, map[string]any{"name": n}); r.Status != 400 {
			t.Fatalf("name %q should be rejected: %d %s", n, r.Status, r.Body)
		}
	}
	if r := alice.Do("POST", s3p, map[string]any{"name": "okay-name", "acl": "bogus"}); r.Status != 400 {
		t.Fatalf("bad acl: %d", r.Status)
	}
	l := alice.Must(200, "GET", s3p, nil)
	if l["total"].(float64) != 1 || len(bob.Must(200, "GET", s3p, nil)["items"].([]any)) != 0 {
		t.Fatalf("list: %v", l)
	}
	if h.Engine.Count("bucket") != 1 {
		t.Fatal("bucket should be registered with the engine")
	}
	alice.Must(200, "PUT", s3p+"/alice-data/acl", map[string]any{"acl": "public-read"})
	alice.Must(200, "PUT", s3p+"/alice-data/versioning", map[string]any{"status": "Enabled"})
	if r := alice.Do("PUT", s3p+"/alice-data/versioning", map[string]any{"status": "Disabled"}); r.Status != 400 {
		t.Fatalf("disable versioning: %d", r.Status)
	}
	got := alice.Must(200, "GET", s3p+"/alice-data", nil)
	if got["acl"] != "public-read" || got["versioning"] != "Enabled" {
		t.Fatalf("get: %v", got)
	}
	d := alice.Must(200, "GET", "/api/v1/dashboard/summary", nil)
	if d["buckets"].(float64) != 1 {
		t.Fatalf("dashboard: %v", d["buckets"])
	}
	alice.Must(204, "DELETE", s3p+"/alice-data", nil)
	alice.Must(404, "GET", s3p+"/alice-data", nil)
	if h.Engine.Count("bucket") != 0 {
		t.Fatal("bucket should be deregistered")
	}
	// the name is free again
	bob.Must(201, "POST", s3p, map[string]any{"name": "alice-data"})
}

func TestS3ObjectRoundTrip(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "files")
	o := put(t, c, "files", "docs/hello.txt", []byte("hello world"), "Content-Type", "text/plain", "x-amz-meta-author", "alice", "X-Amz-Meta-Project", "sim")
	sum := md5.Sum([]byte("hello world"))
	if o["etag"] != hex.EncodeToString(sum[:]) || o["size"].(float64) != 11 || o["version_id"] != "null" || o["storage_class"] != "STANDARD" {
		t.Fatalf("put: %v", o)
	}
	r := c.Do("GET", objPath("files", "docs/hello.txt"), nil)
	if r.Status != 200 || string(r.Body) != "hello world" || r.Header.Get("Content-Type") != "text/plain" ||
		r.Header.Get("x-amz-meta-author") != "alice" || r.Header.Get("x-amz-meta-project") != "sim" ||
		r.Header.Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` || r.Header.Get("x-amz-version-id") != "null" {
		t.Fatalf("get: %d %q %v", r.Status, r.Body, r.Header)
	}
	hd := c.Do("HEAD", objPath("files", "docs/hello.txt"), nil)
	if hd.Status != 200 || len(hd.Body) != 0 || hd.Header.Get("Content-Length") != "11" {
		t.Fatalf("head: %d %v", hd.Status, hd.Header)
	}
	info := c.Must(200, "GET", s3p+"/files/object-info/docs/hello.txt", nil)
	if md := info["metadata"].(map[string]any); md["author"] != "alice" || info["content_type"] != "text/plain" {
		t.Fatalf("info: %v", info)
	}
	if r := c.Do("GET", objPath("files", "docs/hello.txt"), nil, "Range", "bytes=0-4"); r.Status != 206 || string(r.Body) != "hello" {
		t.Fatalf("range: %d %q", r.Status, r.Body)
	}

	// binary integrity
	bin := make([]byte, 3<<20+17)
	rand.New(rand.NewSource(1)).Read(bin)
	put(t, c, "files", "bin/blob.dat", bin)
	if r := c.Do("GET", objPath("files", "bin/blob.dat"), nil); r.Status != 200 || !bytes.Equal(r.Body, bin) || r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("binary mismatch: %d len=%d", r.Status, len(r.Body))
	}

	// path traversal keys are just strings
	for _, k := range []string{"../../etc/passwd", "a/../../b", "/abs/path", "..\\..\\x"} {
		put(t, c, "files", k, []byte("evil"))
		if r := c.Do("GET", objPath("files", k), nil); r.Status != 200 || string(r.Body) != "evil" {
			t.Fatalf("traversal key %q: %d %s", k, r.Status, r.Body)
		}
	}
	hexRe := regexp.MustCompile(`^[0-9a-f]{32}$`)
	for _, f := range blobFiles(t, h, "files") {
		if !hexRe.MatchString(f) {
			t.Fatalf("unexpected file in bucket dir: %q", f)
		}
	}
	if _, err := os.Stat(filepath.Join(h.Cfg.ObjectStorageDir, "..", "..", "etc", "passwd")); err == nil {
		t.Fatal("traversal wrote outside storage dir")
	}

	// invalid keys
	if r := c.Do("PUT", objPath("files", strings.Repeat("k", 1025)), []byte("x")); r.Status != 400 {
		t.Fatalf("long key: %d", r.Status)
	}
	if r := c.Do("PUT", s3p+"/files/objects/", []byte("x")); r.Status != 400 {
		t.Fatalf("empty key: %d", r.Status)
	}

	// max size (harness MaxObjectSize = 10 MiB)
	big := make([]byte, h.Cfg.MaxObjectSize+1)
	if r := c.Do("PUT", objPath("files", "big"), big); r.Status != 413 || r.ErrCode() != "EntityTooLarge" {
		t.Fatalf("max size: %d %s", r.Status, r.Body)
	}
	c.Must(404, "GET", s3p+"/files/object-info/big", nil)

	// console form upload
	var form bytes.Buffer
	bd := "XBOUNDARYX"
	fmt.Fprintf(&form, "--%s\r\nContent-Disposition: form-data; name=\"prefix\"\r\n\r\nup/\r\n", bd)
	fmt.Fprintf(&form, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.txt\"\r\nContent-Type: text/plain\r\n\r\nAAA\r\n", bd)
	fmt.Fprintf(&form, "--%s\r\nContent-Disposition: form-data; name=\"file\"; filename=\"b.txt\"\r\n\r\nBB\r\n--%s--\r\n", bd, bd)
	fr := c.Do("POST", s3p+"/files/upload", form.Bytes(), "Content-Type", "multipart/form-data; boundary="+bd)
	if fr.Status != 200 || len(fr.Map()["objects"].([]any)) != 2 {
		t.Fatalf("form upload: %d %s", fr.Status, fr.Body)
	}
	if r := c.Do("GET", objPath("files", "up/a.txt"), nil); string(r.Body) != "AAA" {
		t.Fatalf("form upload content: %q", r.Body)
	}

	// delete
	c.Must(204, "DELETE", objPath("files", "docs/hello.txt"), nil)
	c.Must(404, "GET", objPath("files", "docs/hello.txt"), nil)
	c.Must(204, "DELETE", objPath("files", "never-existed"), nil)

	st := c.Must(200, "GET", "/api/v1/s3/stats", nil)
	if st["buckets"].(float64) != 1 || st["objects"].(float64) != 7 || st["by_storage_class"].(map[string]any)["STANDARD"] == nil {
		t.Fatalf("stats: %v", st)
	}
}

func TestS3ListingPrefixDelimiterPagination(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "list")
	for _, k := range []string{"a.txt", "b.txt", "photos/2024/x.jpg", "photos/2024/y.jpg", "photos/z.jpg", "docs/r.md", "photosx"} {
		put(t, c, "list", k, []byte(k))
	}
	keys := func(m map[string]any) (ks, ps []string) {
		for _, it := range m["items"].([]any) {
			ks = append(ks, it.(map[string]any)["key"].(string))
		}
		for _, p := range m["common_prefixes"].([]any) {
			ps = append(ps, p.(string))
		}
		return
	}
	m := c.Must(200, "GET", s3p+"/list/objects?delimiter=/", nil)
	ks, ps := keys(m)
	if strings.Join(ks, ",") != "a.txt,b.txt,photosx" || strings.Join(ps, ",") != "docs/,photos/" || m["total"].(float64) != 5 {
		t.Fatalf("root listing: %v %v %v", ks, ps, m["total"])
	}
	m = c.Must(200, "GET", s3p+"/list/objects?prefix=photos/&delimiter=/", nil)
	ks, ps = keys(m)
	if strings.Join(ks, ",") != "photos/z.jpg" || strings.Join(ps, ",") != "photos/2024/" {
		t.Fatalf("prefix listing: %v %v", ks, ps)
	}
	m = c.Must(200, "GET", s3p+"/list/objects?prefix=photos", nil)
	if ks, _ = keys(m); len(ks) != 4 {
		t.Fatalf("prefix without delimiter: %v", ks)
	}
	var all []string
	for page := 1; page <= 3; page++ {
		m = c.Must(200, "GET", fmt.Sprintf("%s/list/objects?page_size=3&page=%d", s3p, page), nil)
		ks, _ = keys(m)
		all = append(all, ks...)
		if m["total"].(float64) != 7 {
			t.Fatalf("total: %v", m["total"])
		}
	}
	if len(all) != 7 || all[0] != "a.txt" || all[6] != "photosx" {
		t.Fatalf("pagination: %v", all)
	}
	m = c.Must(200, "GET", s3p+"/list/objects?delimiter=/&page_size=2&page=2", nil)
	ks, ps = keys(m)
	if len(ks) != 0 || strings.Join(ps, ",") != "docs/,photos/" {
		t.Fatalf("paged delimiter listing: %v %v", ks, ps)
	}
	m = c.Must(200, "GET", s3p+"/list/objects?q=JPG", nil)
	if ks, _ = keys(m); len(ks) != 3 {
		t.Fatalf("search: %v", ks)
	}
}

func TestS3Versioning(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "ver")
	c.Must(200, "PUT", s3p+"/ver/versioning", map[string]any{"status": "Enabled"})
	v1 := put(t, c, "ver", "k", []byte("one"))["version_id"].(string)
	v2 := put(t, c, "ver", "k", []byte("two"))["version_id"].(string)
	if v1 == v2 || v1 == "null" {
		t.Fatalf("versions: %s %s", v1, v2)
	}
	if r := c.Do("GET", objPath("ver", "k"), nil); string(r.Body) != "two" {
		t.Fatalf("latest: %s", r.Body)
	}
	if r := c.Do("GET", objPath("ver", "k")+"?version_id="+v1, nil); string(r.Body) != "one" || r.Header.Get("x-amz-version-id") != v1 {
		t.Fatalf("old version: %s", r.Body)
	}
	// delete → delete marker
	dr := c.Do("DELETE", objPath("ver", "k"), nil)
	if dr.Status != 204 || dr.Header.Get("x-amz-delete-marker") != "true" {
		t.Fatalf("delete: %d %v", dr.Status, dr.Header)
	}
	marker := dr.Header.Get("x-amz-version-id")
	if r := c.Do("GET", objPath("ver", "k"), nil); r.Status != 404 || r.Header.Get("x-amz-delete-marker") != "true" {
		t.Fatalf("get after delete: %d %v", r.Status, r.Header)
	}
	l := c.Must(200, "GET", s3p+"/ver/objects", nil)
	if l["total"].(float64) != 0 {
		t.Fatalf("deleted key listed: %v", l)
	}
	vs := c.Must(200, "GET", s3p+"/ver/versions", nil)
	items := vs["items"].([]any)
	if len(items) != 3 || items[0].(map[string]any)["delete_marker"] != true || items[0].(map[string]any)["is_latest"] != true {
		t.Fatalf("versions: %v", vs)
	}
	// removing the delete marker restores the object
	c.Must(204, "DELETE", objPath("ver", "k")+"?version_id="+marker, nil)
	if r := c.Do("GET", objPath("ver", "k"), nil); string(r.Body) != "two" {
		t.Fatalf("restored: %d %s", r.Status, r.Body)
	}
	// permanently delete v1
	c.Must(204, "DELETE", objPath("ver", "k")+"?version_id="+v1, nil)
	c.Must(404, "GET", objPath("ver", "k")+"?version_id="+v1, nil)
	// suspended: null version replaced in place, older versions kept
	c.Must(200, "PUT", s3p+"/ver/versioning", map[string]any{"status": "Suspended"})
	if put(t, c, "ver", "k", []byte("three"))["version_id"] != "null" {
		t.Fatal("suspended put should create null version")
	}
	put(t, c, "ver", "k", []byte("four"))
	vs = c.Must(200, "GET", s3p+"/ver/versions?prefix=k", nil)
	if vs["total"].(float64) != 2 {
		t.Fatalf("suspended versions (v2 + null): %v", vs)
	}
	if r := c.Do("GET", objPath("ver", "k")+"?version_id="+v2, nil); string(r.Body) != "two" {
		t.Fatalf("v2 kept: %s", r.Body)
	}
	dr = c.Do("DELETE", objPath("ver", "k"), nil)
	if dr.Header.Get("x-amz-version-id") != "null" || dr.Header.Get("x-amz-delete-marker") != "true" {
		t.Fatalf("suspended delete: %v", dr.Header)
	}
	// blobs: only v2 remains on disk
	if n := len(blobFiles(t, h, "ver")); n != 1 {
		t.Fatalf("expected 1 blob on disk, got %d", n)
	}
}

func TestS3Multipart(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "mpu")
	up := c.Must(200, "POST", s3p+"/mpu/multipart", map[string]any{"key": "big/file.bin", "content_type": "application/x-test"})["upload_id"].(string)
	p1 := bytes.Repeat([]byte("A"), 1<<20)
	p2 := []byte("tail")
	e1 := c.Must(200, "PUT", fmt.Sprintf("%s/mpu/multipart/%s/parts/1", s3p, up), p1)["etag"].(string)
	e2 := c.Must(200, "PUT", fmt.Sprintf("%s/mpu/multipart/%s/parts/2", s3p, up), p2)["etag"].(string)
	if r := c.Do("PUT", fmt.Sprintf("%s/mpu/multipart/%s/parts/10001", s3p, up), []byte("x")); r.Status != 400 {
		t.Fatalf("part number: %d", r.Status)
	}
	l := c.Must(200, "GET", s3p+"/mpu/multipart", nil)
	if l["total"].(float64) != 1 {
		t.Fatalf("list uploads: %v", l)
	}
	if r := c.Do("POST", fmt.Sprintf("%s/mpu/multipart/%s/complete", s3p, up), map[string]any{"parts": []map[string]any{{"part_number": 1, "etag": e1}, {"part_number": 2, "etag": "deadbeef"}}}); r.Status != 400 || r.ErrCode() != "InvalidPart" {
		t.Fatalf("bad etag: %d %s", r.Status, r.Body)
	}
	if r := c.Do("POST", fmt.Sprintf("%s/mpu/multipart/%s/complete", s3p, up), map[string]any{"parts": []map[string]any{{"part_number": 2, "etag": e2}, {"part_number": 1, "etag": e1}}}); r.Status != 400 || r.ErrCode() != "InvalidPartOrder" {
		t.Fatalf("bad order: %d %s", r.Status, r.Body)
	}
	o := c.Must(200, "POST", fmt.Sprintf("%s/mpu/multipart/%s/complete", s3p, up), map[string]any{"parts": []map[string]any{{"part_number": 1, "etag": `"` + e1 + `"`}, {"part_number": 2, "etag": e2}}})
	r1, _ := hex.DecodeString(e1)
	r2, _ := hex.DecodeString(e2)
	want := md5.Sum(append(r1, r2...))
	if o["etag"] != hex.EncodeToString(want[:])+"-2" || o["size"].(float64) != float64(len(p1)+len(p2)) || o["content_type"] != "application/x-test" {
		t.Fatalf("complete: %v", o)
	}
	if r := c.Do("GET", objPath("mpu", "big/file.bin"), nil); !bytes.Equal(r.Body, append(p1, p2...)) {
		t.Fatal("multipart content mismatch")
	}
	if c.Must(200, "GET", s3p+"/mpu/multipart", nil)["total"].(float64) != 0 {
		t.Fatal("completed upload still listed")
	}
	if n := len(blobFiles(t, h, "mpu")); n != 1 {
		t.Fatalf("part blobs should be removed after complete, have %d files", n)
	}
	// abort
	up2 := c.Must(200, "POST", s3p+"/mpu/multipart", map[string]any{"key": "x"})["upload_id"].(string)
	c.Must(200, "PUT", fmt.Sprintf("%s/mpu/multipart/%s/parts/1", s3p, up2), []byte("data"))
	c.Must(204, "DELETE", fmt.Sprintf("%s/mpu/multipart/%s", s3p, up2), nil)
	c.Must(404, "DELETE", fmt.Sprintf("%s/mpu/multipart/%s", s3p, up2), nil)
	c.Must(404, "PUT", fmt.Sprintf("%s/mpu/multipart/%s/parts/1", s3p, up2), []byte("data"))
	if n := len(blobFiles(t, h, "mpu")); n != 1 {
		t.Fatalf("aborted part blobs should be removed, have %d files", n)
	}
}

func TestS3BucketPolicies(t *testing.T) {
	h := testutil.New(t)
	root := h.Signup("alice")
	acct := root.AccountID()
	mkBucket(t, root, "secure")
	put(t, root, "secure", "secret.txt", []byte("s3cr3t"))
	put(t, root, "secure", "public/info.txt", []byte("info"))

	full := root.IAMUser("full", "CloudSimS3FullAccess")
	none := root.IAMUser("none")
	// IAM-only checks
	full.Must(200, "GET", s3p+"/secure/objects", nil)
	if r := none.Do("GET", s3p+"/secure/objects", nil); r.Status != 403 || r.ErrCode() != "AccessDenied" {
		t.Fatalf("IAM-denied user: %d %s", r.Status, r.Body)
	}
	if r := none.Do("GET", objPath("secure", "secret.txt"), nil); r.Status != 403 {
		t.Fatalf("IAM-denied get: %d", r.Status)
	}
	// invalid policies
	if r := root.Do("PUT", s3p+"/secure/policy", map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Action": "s3:GetObject", "Resource": "arn:cloudsim:s3:::secure/*"}}}); r.Status != 400 {
		t.Fatalf("policy without principal: %d", r.Status)
	}
	if r := root.Do("PUT", s3p+"/secure/policy", map[string]any{"Version": "2012-10-17", "Statement": []any{map[string]any{"Effect": "Allow", "Principal": "*", "Action": "s3:GetObject", "Resource": "arn:cloudsim:s3:::other/*"}}}); r.Status != 400 {
		t.Fatalf("policy for other bucket: %d", r.Status)
	}
	root.Must(404, "GET", s3p+"/secure/policy", nil)
	pol := map[string]any{"Version": "2012-10-17", "Statement": []any{
		map[string]any{"Sid": "DenyFullSecret", "Effect": "Deny", "Principal": map[string]any{"CloudSim": "arn:cloudsim:iam::" + acct + ":user/full"},
			"Action": "s3:GetObject", "Resource": "arn:cloudsim:s3:::secure/secret.txt"},
		map[string]any{"Sid": "AllowNonePublic", "Effect": "Allow", "Principal": map[string]any{"CloudSim": "arn:cloudsim:iam::" + acct + ":user/none"},
			"Action": "s3:GetObject", "Resource": "arn:cloudsim:s3:::secure/public/*"},
		map[string]any{"Sid": "AllowNoneListPublic", "Effect": "Allow", "Principal": map[string]any{"CloudSim": "arn:cloudsim:iam::" + acct + ":user/none"},
			"Action": "s3:ListBucket", "Resource": "arn:cloudsim:s3:::secure", "Condition": map[string]any{"StringLike": map[string]any{"s3:prefix": "public/*"}}},
	}}
	root.Must(200, "PUT", s3p+"/secure/policy", pol)
	if got := root.Must(200, "GET", s3p+"/secure/policy", nil); len(got["Statement"].([]any)) != 3 {
		t.Fatalf("get policy: %v", got)
	}
	if root.Must(200, "GET", s3p+"/secure", nil)["has_policy"] != true {
		t.Fatal("has_policy")
	}
	// explicit deny beats IAM allow
	if r := full.Do("GET", objPath("secure", "secret.txt"), nil); r.Status != 403 || !strings.Contains(string(r.Body), "explicitDeny") {
		t.Fatalf("bucket policy deny: %d %s", r.Status, r.Body)
	}
	full.Must(200, "GET", objPath("secure", "public/info.txt"), nil)
	// bucket policy allow grants access without identity policies
	if r := none.Do("GET", objPath("secure", "public/info.txt"), nil); r.Status != 200 || string(r.Body) != "info" {
		t.Fatalf("bucket policy allow: %d %s", r.Status, r.Body)
	}
	none.Must(200, "GET", s3p+"/secure/objects?prefix=public/", nil)
	none.Must(403, "GET", s3p+"/secure/objects?prefix=", nil)
	none.Must(403, "GET", objPath("secure", "secret.txt"), nil)
	if r := none.Do("PUT", objPath("secure", "public/new.txt"), []byte("x")); r.Status != 403 {
		t.Fatalf("put not granted: %d", r.Status)
	}
	// root is unaffected by statements that do not name it
	root.Must(200, "GET", objPath("secure", "secret.txt"), nil)
	root.Must(204, "DELETE", s3p+"/secure/policy", nil)
	full.Must(200, "GET", objPath("secure", "secret.txt"), nil)
	h.Srv.FlushAudit()
	a := root.Must(200, "GET", "/api/v1/audit?action=s3:GetObject", nil)
	if len(a["items"].([]any)) == 0 {
		t.Fatalf("denied reads should be audited: %v", a)
	}

	// public-read ACL: any authenticated principal of the platform may GetObject
	other := h.Signup("mallory")
	other.Must(404, "GET", objPath("secure", "public/info.txt"), nil)
	root.Must(200, "PUT", s3p+"/secure/acl", map[string]any{"acl": "public-read"})
	other.Must(200, "GET", objPath("secure", "public/info.txt"), nil)
	other.Must(404, "GET", s3p+"/secure/objects", nil)
	if r := other.Do("PUT", objPath("secure", "x"), []byte("x")); r.Status != 404 {
		t.Fatalf("public-read must not allow writes: %d", r.Status)
	}
}

func TestS3ForceDeleteRemovesBlobs(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "doomed")
	c.Must(200, "PUT", s3p+"/doomed/versioning", map[string]any{"status": "Enabled"})
	for i := 0; i < 3; i++ {
		put(t, c, "doomed", fmt.Sprintf("k%d", i), []byte("data"))
		put(t, c, "doomed", fmt.Sprintf("k%d", i), []byte("data2"))
	}
	up := c.Must(200, "POST", s3p+"/doomed/multipart", map[string]any{"key": "m"})["upload_id"].(string)
	c.Must(200, "PUT", fmt.Sprintf("%s/doomed/multipart/%s/parts/1", s3p, up), []byte("p"))
	if n := len(blobFiles(t, h, "doomed")); n != 7 {
		t.Fatalf("blobs before delete: %d", n)
	}
	if r := c.Do("DELETE", s3p+"/doomed", nil); r.Status != 409 || r.ErrCode() != "BucketNotEmpty" {
		t.Fatalf("non-empty delete: %d %s", r.Status, r.Body)
	}
	c.Must(204, "DELETE", s3p+"/doomed?force=true", nil)
	if _, err := os.Stat(filepath.Join(h.Cfg.ObjectStorageDir, "doomed")); !os.IsNotExist(err) {
		t.Fatalf("bucket directory should be removed: %v", err)
	}
	var n int
	if err := h.DB.QueryRow(`SELECT COUNT(*) FROM s3_objects WHERE bucket='doomed'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("object rows left: %d %v", n, err)
	}
}

func TestS3LifecycleRun(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "lcb")
	put(t, c, "lcb", "logs/a.txt", []byte("old log"))
	put(t, c, "lcb", "logs/keep.txt", []byte("keep"))
	put(t, c, "lcb", "data/b.bin", []byte("archive me"))
	up := c.Must(200, "POST", s3p+"/lcb/multipart", map[string]any{"key": "stale"})["upload_id"].(string)
	if r := c.Do("PUT", s3p+"/lcb/lifecycle", map[string]any{"rules": []any{map[string]any{"id": "x", "status": "Enabled", "prefix": "logs/"}}}); r.Status != 400 {
		t.Fatalf("rule without action: %d", r.Status)
	}
	if r := c.Do("PUT", s3p+"/lcb/lifecycle", map[string]any{"rules": []any{map[string]any{"id": "x", "expiration_days": 1, "transitions": []any{map[string]any{"days": 1, "storage_class": "FLOPPY"}}}}}); r.Status != 400 {
		t.Fatalf("bad storage class: %d", r.Status)
	}
	c.Must(200, "PUT", s3p+"/lcb/lifecycle", map[string]any{"rules": []any{
		map[string]any{"id": "expire-logs", "status": "Enabled", "prefix": "logs/", "expiration_days": 30},
		map[string]any{"id": "archive", "status": "Enabled", "prefix": "data/", "transitions": []any{map[string]any{"days": 30, "storage_class": "GLACIER"}}, "abort_incomplete_multipart_days": 7},
	}})
	if rules := c.Must(200, "GET", s3p+"/lcb/lifecycle", nil)["rules"].([]any); len(rules) != 2 {
		t.Fatalf("lifecycle rules: %v", rules)
	}
	var mu sync.Mutex
	var seen map[string]any
	h.Engine.Lifecycle = func(body map[string]any) map[string]any {
		mu.Lock()
		seen = body
		mu.Unlock()
		return map[string]any{"actions": []any{
			map[string]any{"type": "expire", "key": "logs/a.txt", "version_id": "null", "rule_id": "expire-logs"},
			map[string]any{"type": "transition", "key": "data/b.bin", "version_id": "null", "storage_class": "GLACIER", "rule_id": "archive"},
			map[string]any{"type": "abort_multipart", "key": "stale", "upload_id": up, "rule_id": "archive"},
			map[string]any{"type": "expire", "key": "logs/missing.txt", "rule_id": "expire-logs"},
		}}
	}
	res := c.Must(200, "POST", s3p+"/lcb/lifecycle/run", nil)
	if res["applied"].(float64) != 3 || res["evaluated_objects"].(float64) != 3 || res["evaluated_uploads"].(float64) != 1 {
		t.Fatalf("lifecycle run: %v", res)
	}
	mu.Lock()
	if len(seen["rules"].([]any)) != 2 || len(seen["objects"].([]any)) != 3 || seen["now"] == nil {
		t.Fatalf("engine request: %v", seen)
	}
	mu.Unlock()
	c.Must(404, "GET", objPath("lcb", "logs/a.txt"), nil)
	c.Must(200, "GET", objPath("lcb", "logs/keep.txt"), nil)
	if info := c.Must(200, "GET", s3p+"/lcb/object-info/data/b.bin", nil); info["storage_class"] != "GLACIER" {
		t.Fatalf("transition: %v", info)
	}
	if c.Must(200, "GET", s3p+"/lcb/multipart", nil)["total"].(float64) != 0 {
		t.Fatal("stale upload should be aborted")
	}
	ev := c.Must(200, "GET", "/api/v1/events?resource_id=lcb", nil)
	found := false
	for _, e := range ev["items"].([]any) {
		if e.(map[string]any)["type"] == "lifecycle_applied" {
			found = true
		}
	}
	if !found {
		t.Fatalf("lifecycle event missing: %v", ev)
	}
}

func TestS3ConcurrentUploadsSameKey(t *testing.T) {
	h := testutil.New(t)
	c := h.Signup("alice")
	mkBucket(t, c, "race")
	const n = 12
	bodies := map[string]bool{}
	var wg sync.WaitGroup
	statuses := make([]int, n)
	for i := 0; i < n; i++ {
		body := bytes.Repeat([]byte{byte('a' + i)}, 64<<10)
		bodies[string(body)] = true
		wg.Add(1)
		go func(i int, body []byte) {
			defer wg.Done()
			statuses[i] = c.Do("PUT", objPath("race", "same"), body).Status
		}(i, body)
	}
	wg.Wait()
	for i, s := range statuses {
		if s != 200 {
			t.Fatalf("upload %d: %d", i, s)
		}
	}
	r := c.Do("GET", objPath("race", "same"), nil)
	sum := md5.Sum(r.Body)
	if !bodies[string(r.Body)] || r.Header.Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` {
		t.Fatal("final object is not one of the uploaded bodies or etag mismatch")
	}
	if v := c.Must(200, "GET", s3p+"/race/versions", nil); v["total"].(float64) != 1 {
		t.Fatalf("unversioned bucket should keep one version: %v", v["total"])
	}
	if files := blobFiles(t, h, "race"); len(files) != 1 {
		t.Fatalf("orphaned blobs: %v", files)
	}
}
