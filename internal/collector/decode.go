package collector

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/RaafidAfraazG/NodeWatch/internal/metrics"
)

// checkValue rejects duplicate keys, nulls, and excessive nesting.
func checkValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("JSON nesting too deep")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return errors.New("null values are not supported")
	}
	if delim, ok := t.(json.Delim); ok {
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				k, ok := key.(string)
				if !ok || seen[k] {
					return errors.New("duplicate or invalid JSON key")
				}
				seen[k] = true
				if err = checkValue(d, depth+1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := checkValue(d, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	return nil
}

func object(data []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, k := range required {
		allowed[k] = true
		if _, ok := values[k]; !ok {
			return nil, errors.New("missing required field: " + k)
		}
	}
	for _, k := range optional {
		allowed[k] = true
	}
	for k := range values {
		if !allowed[k] {
			return nil, errors.New("unknown JSON field")
		}
	}
	return values, nil
}

func decodeSnapshot(data []byte) (metrics.Snapshot, error) {
	var s metrics.Snapshot
	if !utf8.Valid(data) {
		return s, errors.New("invalid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := checkValue(d, 0); err != nil {
		return s, err
	}
	if _, err := d.Token(); err != io.EOF {
		return s, errors.New("expected exactly one JSON object")
	}
	v, err := object(data, []string{"schema_version", "node_id", "os", "architecture", "collected_at"}, []string{"hostname", "cpu", "memory", "load", "filesystems", "disks", "errors"})
	if err != nil {
		return s, err
	}
	for _, item := range []struct {
		name   string
		fields []string
		array  bool
	}{
		{"cpu", []string{"utilization_percent"}, false},
		{"memory", []string{"total_bytes", "available_bytes", "used_percent"}, false},
		{"load", []string{"one", "five", "fifteen"}, false},
		{"filesystems", []string{"mount_point", "total_bytes", "free_bytes", "available_bytes", "used_percent"}, true},
		{"disks", []string{"device", "reads_completed", "writes_completed", "read_bytes", "written_bytes"}, true},
	} {
		if raw, ok := v[item.name]; ok {
			entries := []json.RawMessage{raw}
			if item.array {
				if err = json.Unmarshal(raw, &entries); err != nil {
					return s, err
				}
			}
			for _, entry := range entries {
				if _, err = object(entry, item.fields, nil); err != nil {
					return s, err
				}
			}
		}
	}
	if raw, ok := v["cpu"]; ok {
		if _, err = object(raw, []string{"utilization_percent"}, []string{"iowait_percent"}); err != nil {
			return s, err
		}
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	err = d.Decode(&s)
	s.CollectedAt = s.CollectedAt.UTC()
	return s, err
}
