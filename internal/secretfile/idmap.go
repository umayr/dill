package secretfile

import (
	"bufio"
	"io"
	"strconv"
	"strings"

	"github.com/umayr/dill/internal/config"
)

// ParseIDMap parses the kernel's numeric uid_map/gid_map representation. It
// validates the whole map, including overlapping host ranges, before use.
func ParseIDMap(r io.Reader) ([]IDRange, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, 65537))
	scanner.Buffer(make([]byte, 1024), 65537)
	var ranges []IDRange
	total := 0
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line) + 1
		if total > 65536 || len(ranges) >= 340 {
			return nil, config.ErrSecretUnavailable
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			return nil, config.ErrSecretUnavailable
		}
		var vals [3]uint64
		for i, p := range parts {
			if p == "" || strings.Trim(p, "0123456789") != "" {
				return nil, config.ErrSecretUnavailable
			}
			v, e := strconv.ParseUint(p, 10, 32)
			if e != nil {
				return nil, config.ErrSecretUnavailable
			}
			vals[i] = v
		}
		x := IDRange{Container: vals[0], Host: vals[1], Size: vals[2]}
		if x.Size == 0 || x.Container+x.Size > 4294967295 || x.Host+x.Size > 4294967295 {
			return nil, config.ErrSecretUnavailable
		}
		for _, old := range ranges {
			if overlaps(x.Container, x.Size, old.Container, old.Size) || overlaps(x.Host, x.Size, old.Host, old.Size) {
				return nil, config.ErrSecretUnavailable
			}
		}
		ranges = append(ranges, x)
	}
	if scanner.Err() != nil || len(ranges) == 0 {
		return nil, config.ErrSecretUnavailable
	}
	return ranges, nil
}
func overlaps(a, n, b, m uint64) bool { return a < b+m && b < a+n }
