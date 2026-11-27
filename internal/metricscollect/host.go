package metricscollect

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/umayr/dill/internal/metricsmodel"
)

// MaxMeminfoBytes is the trial per-file parser limit, including unknown lines.
const MaxMeminfoBytes = 16 << 10

var errHostMemory = errors.New("host memory observation unavailable")

// HostSource observes one host memory pair on an acquisition's bound daemon.
type HostSource func(context.Context, ReadBudget) (metricsmodel.HostObservation, error)

// newHostMemorySource builds a host source from an injected fresh Linux
// meminfo opener. It returns nil when locality was not independently proven
// for the enrolled daemon or no opener was supplied. It opens no file itself
// and never infers locality from a path or an engine ID.
//
// Call this from the verifier for a binding whose locality and source timing
// have been qualified. Open must provide one fresh native generation, not a
// cache or proxy, and keep the read and close bounded under the supplied
// context. If open returns a reader with an error, the source still closes
// it. No named path or default native opener is supplied here.
func newHostMemorySource(localityVerified bool, open func(context.Context) (io.ReadCloser, error)) HostSource {
	if !localityVerified || open == nil {
		return nil
	}
	return func(ctx context.Context, budget ReadBudget) (metricsmodel.HostObservation, error) {
		var h metricsmodel.HostObservation
		err := budget.Do(func(ctx context.Context) (err error) {
			r, err := open(ctx)
			if r != nil {
				defer func() {
					if closeErr := r.Close(); closeErr != nil {
						err = errors.Join(err, closeErr)
					}
				}()
			}
			if err != nil {
				return err // Preserve binding loss for the coordinator.
			}
			if r == nil {
				return errHostMemory
			}
			// Budget first: the one-byte local limit probe is charged too.
			data, err := io.ReadAll(io.LimitReader(budget.Reader(r), MaxMeminfoBytes+1))
			observedAt := time.Now()
			if err != nil {
				return err
			}
			if len(data) > MaxMeminfoBytes {
				return errHostMemory
			}
			total, available, ok := parseMeminfo(data)
			if !ok {
				return errHostMemory
			}
			h = metricsmodel.HostObservation{
				LocalityVerified: true, SameRead: true,
				SourceTime:  metricsmodel.SourceTime{At: observedAt, Verified: true},
				TotalKB:     metricsmodel.UintReading{Value: total, Present: true},
				AvailableKB: metricsmodel.UintReading{Value: available, Present: true},
			}
			return nil
		})
		if err != nil {
			return metricsmodel.HostObservation{}, err
		}
		if err := ctx.Err(); err != nil {
			return metricsmodel.HostObservation{}, err
		}
		return h, nil
	}
}

func parseMeminfo(data []byte) (total, available uint64, ok bool) {
	// Linux procfs emits complete newline-terminated lines. The full file
	// must be read so a late duplicate cannot evade classification.
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return 0, 0, false
	}
	var totalSeen, availableSeen bool
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		key, value, colon := bytes.Cut(line, []byte{':'})
		var dst *uint64
		var seen *bool
		switch string(bytes.Trim(key, " \t")) {
		case "MemTotal":
			dst, seen = &total, &totalSeen
		case "MemAvailable":
			dst, seen = &available, &availableSeen
		default:
			continue // Other kernel fields are not part of the observation.
		}
		if *seen || !colon {
			return 0, 0, false
		}
		fields := bytes.Fields(value)
		if len(fields) != 2 || !bytes.Equal(fields[1], []byte("kB")) {
			return 0, 0, false
		}
		v, err := strconv.ParseUint(string(fields[0]), 10, 64)
		if err != nil {
			return 0, 0, false
		}
		*seen, *dst = true, v
	}
	return total, available, totalSeen && availableSeen && total > 0 && available <= total
}
