package agentcmd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/umayr/dill/internal/hostagent"
)

type dockerLogReader struct {
	body      io.ReadCloser
	tty       bool
	remaining uint32
	stream    string
}

func (d *dockerLogReader) Close() error { return d.body.Close() }
func (d *dockerLogReader) Next() (string, []byte, error) {
	if d.tty {
		b := make([]byte, 16384)
		n, e := d.body.Read(b)
		if n > 0 {
			return "unknown", b[:n], nil
		}
		return "", nil, e
	}
	for d.remaining == 0 {
		var header [8]byte
		if _, e := io.ReadFull(d.body, header[:]); e != nil {
			return "", nil, e
		}
		if header[1] != 0 || header[2] != 0 || header[3] != 0 || (header[0] != 1 && header[0] != 2) {
			return "", nil, errors.New("invalid docker log frame")
		}
		d.stream = "stdout"
		if header[0] == 2 {
			d.stream = "stderr"
		}
		d.remaining = binary.BigEndian.Uint32(header[4:])
	}
	n := d.remaining
	if n > 16384 {
		n = 16384
	}
	b := make([]byte, n)
	if _, e := io.ReadFull(d.body, b); e != nil {
		if errors.Is(e, io.EOF) {
			e = io.ErrUnexpectedEOF
		}
		return "", nil, e
	}
	d.remaining -= n
	return d.stream, b, nil
}
func (d dockerIdentity) OpenLogs(ctx context.Context, id string, tail int, follow, timestamps bool) (hostagent.LogReader, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", "http://engine/containers/"+id+"/json", nil)
	if e != nil {
		return nil, e
	}
	resp, e := d.client.Do(req)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, hostagent.ErrNotFound
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, errors.New("engine unavailable")
	}
	var v struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
			TTY    bool              `json:"Tty"`
		} `json:"Config"`
		HostConfig struct {
			LogConfig struct {
				Type string `json:"Type"`
			} `json:"LogConfig"`
		} `json:"HostConfig"`
	}
	e = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(&v)
	resp.Body.Close()
	if e != nil {
		return nil, e
	}
	if v.ID != id || v.Config.Labels["dill.managed"] != "true" || v.Config.Labels["dill.stack"] == "" || v.Config.Labels["dill.service"] == "" {
		return nil, hostagent.ErrNotFound
	}
	if v.HostConfig.LogConfig.Type != "json-file" && v.HostConfig.LogConfig.Type != "local" {
		return nil, hostagent.ErrUnsupported
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {strconv.Itoa(tail)}, "follow": {strconv.FormatBool(follow)}, "timestamps": {strconv.FormatBool(timestamps)}}
	req, e = http.NewRequestWithContext(ctx, "GET", "http://engine/containers/"+id+"/logs?"+q.Encode(), nil)
	if e != nil {
		return nil, e
	}
	client := *d.client
	client.Timeout = 0
	resp, e = client.Do(req)
	if e != nil {
		return nil, e
	}
	if resp.StatusCode == 404 {
		resp.Body.Close()
		return nil, hostagent.ErrNotFound
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		return nil, errors.New("engine unavailable")
	}
	return &dockerLogReader{body: resp.Body, tty: v.Config.TTY}, nil
}
