package commands

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path"
	"strings"
)

// LocalURI is a CloudSim object-store location: local://bucket/key.
type LocalURI struct{ Bucket, Key string }

const uriScheme = "local://"

// ParseLocalURI parses local://bucket[/key]. s3:// is rejected on purpose: CloudSim never touches real S3.
func ParseLocalURI(s string) (LocalURI, bool, error) {
	if strings.HasPrefix(s, "s3://") {
		return LocalURI{}, false, usagef("%s: CloudSim uses local:// URIs for its simulated object store (e.g. local://my-bucket/key)", s)
	}
	if !strings.HasPrefix(s, uriScheme) {
		return LocalURI{}, false, nil
	}
	rest := strings.TrimPrefix(s, uriScheme)
	b, k, _ := strings.Cut(rest, "/")
	if b == "" {
		return LocalURI{}, true, usagef("%s: missing bucket name", s)
	}
	return LocalURI{b, k}, true, nil
}

func mustURI(s string) (LocalURI, error) {
	u, ok, err := ParseLocalURI(s)
	if err != nil {
		return u, err
	}
	if !ok {
		return u, usagef("%s: expected a local://bucket[/key] URI", s)
	}
	return u, nil
}

func contentType(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func (x *Exec) putObject(bucket, key string, data []byte, ctype string) error {
	if ctype == "" {
		ctype = contentType(key)
	}
	if data == nil {
		data = []byte{}
	}
	_, _, err := x.Raw("PUT", "/s3/buckets/"+esc(bucket)+"/objects/"+escKey(key), data, map[string]string{"Content-Type": ctype})
	return err
}

func (x *Exec) getObject(bucket, key, version string) ([]byte, map[string]string, error) {
	p := "/s3/buckets/" + esc(bucket) + "/objects/" + escKey(key)
	if version != "" {
		p += "?version_id=" + url.QueryEscape(version)
	}
	return x.Raw("GET", p, nil, nil)
}

func (x *Exec) listObjects(bucket, prefix, delimiter string) ([]any, []any, error) {
	q := url.Values{}
	if prefix != "" {
		q.Set("prefix", prefix)
	}
	if delimiter != "" {
		q.Set("delimiter", delimiter)
	}
	q.Set("page_size", "200")
	var items, prefixes []any
	for page := 1; page <= 100; page++ {
		q.Set("page", fmt.Sprint(page))
		v, err := x.Call("GET", "/s3/buckets/"+esc(bucket)+"/objects?"+q.Encode(), nil)
		if err != nil {
			return nil, nil, err
		}
		m, _ := v.(map[string]any)
		it, _ := m["items"].([]any)
		items = append(items, it...)
		if page == 1 {
			prefixes, _ = m["common_prefixes"].([]any)
		}
		if len(it) == 0 || len(items) >= toInt(m["total"]) {
			break
		}
	}
	return items, prefixes, nil
}

func lsTime(v any) string {
	s := scalarString(v)
	if len(s) >= 19 {
		return strings.Replace(s[:19], "T", " ", 1)
	}
	return s
}

func joinKey(prefix, name string) string {
	if prefix == "" || strings.HasSuffix(prefix, "/") {
		return prefix + name
	}
	return prefix + "/" + name
}

func s3cp(x *Exec) (any, error) {
	src, dst := x.Args[0], x.Args[1]
	su, sIsURI, err := ParseLocalURI(src)
	if err != nil {
		return nil, err
	}
	du, dIsURI, err := ParseLocalURI(dst)
	if err != nil {
		return nil, err
	}
	recursive, _ := x.Bool("recursive")
	out := x.Env.Out
	switch {
	case !sIsURI && dIsURI:
		if x.Env.FS == nil {
			_, err := x.readLocal(src)
			return nil, err
		}
		if recursive || x.Env.FS.IsDir(src) {
			if !recursive {
				return nil, usagef("%s is a directory; use --recursive", src)
			}
			return nil, x.Env.FS.Walk(src, func(rel string) error {
				b, err := x.Env.FS.ReadFile(path.Join(src, rel))
				if err != nil {
					return err
				}
				key := joinKey(du.Key, rel)
				if err := x.putObject(du.Bucket, key, b, x.Str("content-type")); err != nil {
					return err
				}
				fmt.Fprintf(out, "upload: %s to %s%s/%s\n", path.Join(src, rel), uriScheme, du.Bucket, key)
				return nil
			})
		}
		b, err := x.readLocal(src)
		if err != nil {
			return nil, err
		}
		key := du.Key
		if key == "" || strings.HasSuffix(key, "/") {
			key += path.Base(src)
		}
		if err := x.putObject(du.Bucket, key, b, x.Str("content-type")); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "upload: %s to %s%s/%s\n", src, uriScheme, du.Bucket, key)
	case sIsURI && !dIsURI:
		if x.Env.FS == nil {
			_, err := x.readLocal(dst)
			return nil, err
		}
		if su.Key == "" {
			return nil, usagef("source must name an object: local://bucket/key")
		}
		b, _, err := x.getObject(su.Bucket, su.Key, "")
		if err != nil {
			return nil, err
		}
		target := dst
		if strings.HasSuffix(dst, "/") || x.Env.FS.IsDir(dst) {
			target = path.Join(dst, path.Base(su.Key))
		}
		if err := x.Env.FS.WriteFile(target, b); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "download: %s to %s\n", src, target)
	case sIsURI && dIsURI:
		b, h, err := x.getObject(su.Bucket, su.Key, "")
		if err != nil {
			return nil, err
		}
		key := du.Key
		if key == "" || strings.HasSuffix(key, "/") {
			key += path.Base(su.Key)
		}
		if err := x.putObject(du.Bucket, key, b, h["Content-Type"]); err != nil {
			return nil, err
		}
		fmt.Fprintf(out, "copy: %s to %s%s/%s\n", src, uriScheme, du.Bucket, key)
	default:
		return nil, usagef("at least one of source or destination must be a local:// URI")
	}
	return nil, nil
}

func init() {
	register("s3", "high-level commands for the simulated object store (local:// URIs)",
		&Command{Name: "mb", Summary: "Make a bucket", Args: "local://<bucket>", MinArgs: 1, MaxArgs: 1, Run: func(x *Exec) (any, error) {
			u, err := mustURI(x.Args[0])
			if err != nil {
				return nil, err
			}
			if _, err := x.Call("POST", "/s3/buckets", map[string]any{"name": u.Bucket}); err != nil {
				return nil, err
			}
			fmt.Fprintf(x.Env.Out, "make_bucket: %s\n", u.Bucket)
			return nil, nil
		}},
		&Command{Name: "rb", Summary: "Remove a bucket", Args: "local://<bucket>", MinArgs: 1, MaxArgs: 1, Flags: []Flag{{Name: "force", Kind: Bool, Help: "delete all objects first"}},
			Run: func(x *Exec) (any, error) {
				u, err := mustURI(x.Args[0])
				if err != nil {
					return nil, err
				}
				p := "/s3/buckets/" + esc(u.Bucket)
				if f, _ := x.Bool("force"); f {
					p += "?force=true"
				}
				if _, err := x.Call("DELETE", p, nil); err != nil {
					return nil, err
				}
				fmt.Fprintf(x.Env.Out, "remove_bucket: %s\n", u.Bucket)
				return nil, nil
			}},
		&Command{Name: "ls", Summary: "List buckets, or objects under a prefix", Args: "[local://<bucket>[/prefix]]", MaxArgs: 1,
			Flags: []Flag{{Name: "recursive", Kind: Bool, Help: "list all keys below the prefix"}},
			Run: func(x *Exec) (any, error) {
				if len(x.Args) == 0 {
					items, err := x.ListAll("/s3/buckets", nil)
					if err != nil {
						return nil, err
					}
					for _, it := range items {
						m, _ := it.(map[string]any)
						fmt.Fprintf(x.Env.Out, "%s %s\n", lsTime(m["created_at"]), scalarString(m["name"]))
					}
					return nil, nil
				}
				u, err := mustURI(x.Args[0])
				if err != nil {
					return nil, err
				}
				delim := "/"
				if r, _ := x.Bool("recursive"); r {
					delim = ""
				}
				items, prefixes, err := x.listObjects(u.Bucket, u.Key, delim)
				if err != nil {
					return nil, err
				}
				for _, p := range prefixes {
					fmt.Fprintf(x.Env.Out, "%30s PRE %s\n", "", strings.TrimPrefix(scalarString(p), u.Key))
				}
				for _, it := range items {
					m, _ := it.(map[string]any)
					k := scalarString(m["key"])
					if delim != "" {
						k = strings.TrimPrefix(k, u.Key)
					}
					fmt.Fprintf(x.Env.Out, "%s %10s %s\n", lsTime(m["last_modified"]), scalarString(m["size"]), k)
				}
				return nil, nil
			}},
		&Command{Name: "cp", Summary: "Copy a local file to/from the simulated object store", Args: "<LocalPath|LocalUri> <LocalPath|LocalUri>", MinArgs: 2, MaxArgs: 2,
			Flags: []Flag{{Name: "recursive", Kind: Bool, Help: "copy a directory"}, opt("content-type", "override content type")}, Run: s3cp,
			Examples: []string{"cloudsim s3 cp ./index.html local://my-site/index.html", "cloudsim s3 cp local://my-site/index.html ./copy.html"}},
		&Command{Name: "rm", Summary: "Delete an object (or a prefix with --recursive)", Args: "local://<bucket>/<key>", MinArgs: 1, MaxArgs: 1,
			Flags: []Flag{{Name: "recursive", Kind: Bool, Help: "delete every key under the prefix"}},
			Run: func(x *Exec) (any, error) {
				u, err := mustURI(x.Args[0])
				if err != nil {
					return nil, err
				}
				keys := []string{u.Key}
				if r, _ := x.Bool("recursive"); r {
					items, _, err := x.listObjects(u.Bucket, u.Key, "")
					if err != nil {
						return nil, err
					}
					keys = keys[:0]
					for _, it := range items {
						m, _ := it.(map[string]any)
						keys = append(keys, scalarString(m["key"]))
					}
				} else if u.Key == "" {
					return nil, usagef("specify an object key or use --recursive")
				}
				for _, k := range keys {
					if _, err := x.Call("DELETE", "/s3/buckets/"+esc(u.Bucket)+"/objects/"+escKey(k), nil); err != nil {
						return nil, err
					}
					fmt.Fprintf(x.Env.Out, "delete: %s%s/%s\n", uriScheme, u.Bucket, k)
				}
				return nil, nil
			}},
		&Command{Name: "sync", Summary: "Upload new and changed files from a local directory", Args: "<LocalDir> local://<bucket>[/prefix]", MinArgs: 2, MaxArgs: 2,
			Run: func(x *Exec) (any, error) {
				src := x.Args[0]
				du, err := mustURI(x.Args[1])
				if err != nil {
					return nil, err
				}
				if x.Env.FS == nil {
					_, err := x.readLocal(src)
					return nil, err
				}
				items, _, err := x.listObjects(du.Bucket, du.Key, "")
				if err != nil {
					return nil, err
				}
				remote := map[string]string{}
				for _, it := range items {
					m, _ := it.(map[string]any)
					remote[scalarString(m["key"])] = strings.Trim(scalarString(m["etag"]), `"`)
				}
				err = x.Env.FS.Walk(src, func(rel string) error {
					b, err := x.Env.FS.ReadFile(path.Join(src, rel))
					if err != nil {
						return err
					}
					key := joinKey(du.Key, rel)
					sum := md5.Sum(b)
					if remote[key] == hex.EncodeToString(sum[:]) {
						return nil
					}
					if err := x.putObject(du.Bucket, key, b, ""); err != nil {
						return err
					}
					fmt.Fprintf(x.Env.Out, "upload: %s to %s%s/%s\n", path.Join(src, rel), uriScheme, du.Bucket, key)
					return nil
				})
				return nil, err
			}},
	)

	bucket := req("bucket", "bucket name")
	key := req("key", "object key")
	register("s3api", "low-level object store API (simulated)",
		simple("create-bucket", "Create a bucket", "POST", func(x *Exec) string { return "/s3/buckets" },
			func(x *Exec) (any, error) { return map[string]any{"name": x.Str("bucket"), "acl": x.Str("acl")}, nil }, bucket, opt("acl", "private|public-read")),
		deleteCmd("delete-bucket", "Delete an empty bucket", "bucket", "/s3/buckets/"),
		listCmd("list-buckets", "List buckets", "/s3/buckets", "", "", []string{"name", "created_at", "versioning", "object_count", "size_bytes"}),
		simple("head-bucket", "Show bucket details", "GET", func(x *Exec) string { return "/s3/buckets/" + esc(x.Str("bucket")) }, nil, bucket),
		simple("put-bucket-versioning", "Enable or suspend versioning", "PUT", func(x *Exec) string { return "/s3/buckets/" + esc(x.Str("bucket")) + "/versioning" },
			func(x *Exec) (any, error) {
				v := x.Str("versioning-configuration")
				st := v
				if m := Shorthand(v); len(m["Status"]) > 0 {
					st = m["Status"][0]
				} else {
					var j struct {
						Status string `json:"Status"`
					}
					if json.Unmarshal([]byte(v), &j) == nil && j.Status != "" {
						st = j.Status
					}
				}
				return map[string]any{"status": st}, nil
			}, bucket, req("versioning-configuration", "Status=Enabled|Suspended")),
		&Command{Name: "get-bucket-versioning", Summary: "Show versioning status", Flags: []Flag{bucket}, Run: func(x *Exec) (any, error) {
			v, err := x.Call("GET", "/s3/buckets/"+esc(x.Str("bucket")), nil)
			if err != nil {
				return nil, err
			}
			m, _ := v.(map[string]any)
			return map[string]any{"status": m["versioning"]}, nil
		}},
		&Command{Name: "put-object", Summary: "Upload an object", Flags: []Flag{bucket, key, opt("body", "local file to upload"), opt("content", "inline text body (CloudSim extension; works in the web terminal)"), opt("content-type", "content type")},
			Run: func(x *Exec) (any, error) {
				var data []byte
				switch {
				case x.Has("body"):
					b, err := x.readLocal(strings.TrimPrefix(x.Str("body"), "file://"))
					if err != nil {
						return nil, err
					}
					data = b
				default:
					data = []byte(x.Str("content"))
				}
				ct := x.Str("content-type")
				if ct == "" && !x.Has("body") {
					ct = "text/plain"
				}
				if err := x.putObject(x.Str("bucket"), x.Str("key"), data, ct); err != nil {
					return nil, err
				}
				return x.Call("GET", "/s3/buckets/"+esc(x.Str("bucket"))+"/object-info/"+escKey(x.Str("key")), nil)
			}},
		&Command{Name: "get-object", Summary: "Download an object to a file ('-' prints it)", Args: "<outfile>", MinArgs: 1, MaxArgs: 1, Flags: []Flag{bucket, key, opt("version-id", "version")},
			Run: func(x *Exec) (any, error) {
				b, h, err := x.getObject(x.Str("bucket"), x.Str("key"), x.Str("version-id"))
				if err != nil {
					return nil, err
				}
				if x.Args[0] == "-" {
					x.Env.Out.Write(b)
					if len(b) > 0 && b[len(b)-1] != '\n' {
						fmt.Fprintln(x.Env.Out)
					}
					return nil, nil
				}
				if x.Env.FS == nil {
					return nil, errors.New("local file access is not available in the web terminal; use '-' as outfile to print the object")
				}
				if err := x.Env.FS.WriteFile(x.Args[0], b); err != nil {
					return nil, err
				}
				return map[string]any{"content_length": len(b), "content_type": h["Content-Type"], "etag": h["Etag"], "version_id": h["X-Amz-Version-Id"]}, nil
			}},
		simple("head-object", "Show object metadata", "GET", func(x *Exec) string {
			p := "/s3/buckets/" + esc(x.Str("bucket")) + "/object-info/" + escKey(x.Str("key"))
			if v := x.Str("version-id"); v != "" {
				p += "?version_id=" + url.QueryEscape(v)
			}
			return p
		}, nil, bucket, key, opt("version-id", "version")),
		&Command{Name: "delete-object", Summary: "Delete an object (or one version)", Flags: []Flag{bucket, key, opt("version-id", "version")}, Run: func(x *Exec) (any, error) {
			p := "/s3/buckets/" + esc(x.Str("bucket")) + "/objects/" + escKey(x.Str("key"))
			if v := x.Str("version-id"); v != "" {
				p += "?version_id=" + url.QueryEscape(v)
			}
			return x.Call("DELETE", p, nil)
		}},
		&Command{Name: "list-objects-v2", Summary: "List objects", Flags: []Flag{bucket, opt("prefix", "key prefix"), opt("delimiter", "delimiter, e.g. /")},
			Columns: []string{"key", "size", "last_modified", "storage_class"}, Run: func(x *Exec) (any, error) {
				items, pre, err := x.listObjects(x.Str("bucket"), x.Str("prefix"), x.Str("delimiter"))
				if err != nil {
					return nil, err
				}
				if items == nil {
					items = []any{}
				}
				return map[string]any{"items": items, "common_prefixes": pre}, nil
			}},
		&Command{Name: "list-object-versions", Summary: "List all object versions and delete markers", Flags: []Flag{bucket, opt("prefix", "key prefix")},
			Columns: []string{"key", "version_id", "is_latest", "delete_marker", "size", "last_modified"}, Run: func(x *Exec) (any, error) {
				q := url.Values{}
				if p := x.Str("prefix"); p != "" {
					q.Set("prefix", p)
				}
				items, err := x.ListAll("/s3/buckets/"+esc(x.Str("bucket"))+"/versions", q)
				if err != nil {
					return nil, err
				}
				if items == nil {
					items = []any{}
				}
				return map[string]any{"items": items}, nil
			}},
		&Command{Name: "put-bucket-policy", Summary: "Set a bucket policy", Flags: []Flag{bucket, req("policy", "policy JSON or file://policy.json")}, Run: func(x *Exec) (any, error) {
			doc, err := x.Value("policy")
			if err != nil {
				return nil, err
			}
			_, _, err = x.Raw("PUT", "/s3/buckets/"+esc(x.Str("bucket"))+"/policy", []byte(doc), nil)
			return nil, err
		}},
		simple("get-bucket-policy", "Show the bucket policy", "GET", func(x *Exec) string { return "/s3/buckets/" + esc(x.Str("bucket")) + "/policy" }, nil, bucket),
		deleteCmd("delete-bucket-policy", "Remove the bucket policy", "bucket", "/s3/buckets/"),
		simple("put-bucket-acl", "Set the bucket ACL", "PUT", func(x *Exec) string { return "/s3/buckets/" + esc(x.Str("bucket")) + "/acl" },
			func(x *Exec) (any, error) { return map[string]any{"acl": x.Str("acl")}, nil }, bucket, req("acl", "private|public-read")),
	)
	// delete-bucket-policy must hit /policy
	registry["s3api"].Commands["delete-bucket-policy"].Run = func(x *Exec) (any, error) {
		_, err := x.Call("DELETE", "/s3/buckets/"+esc(x.Str("bucket"))+"/policy", nil)
		return nil, err
	}
}
