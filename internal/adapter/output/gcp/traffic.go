package gcp

// trafficEntry represents a revision and its traffic percentage.
// Used to decouple activeRevision selection logic from Cloud Run protobuf types.
type trafficEntry struct {
	revision string
	percent  int32
}

// selectActiveRevision picks the revision with the highest traffic percentage,
// excluding the target revision. Returns empty string if no suitable revision found.
//
// This ensures that during a canary release, the revision receiving the remaining
// traffic (100 - canary%) is always the one currently serving the most traffic,
// not an arbitrary earlier revision from the traffic list.
func selectActiveRevision(traffic []trafficEntry, targetRevision string) string {
	var best string
	var maxPercent int32
	for _, t := range traffic {
		if t.revision == targetRevision {
			continue
		}
		if t.percent > maxPercent {
			best = t.revision
			maxPercent = t.percent
		}
	}
	return best
}

// isTrafficAlreadyMatching checks if the current traffic state already has
// the target revision at the desired percent. Used for idempotent ShiftTraffic:
// if the state already matches, the update can be skipped.
func isTrafficAlreadyMatching(traffic []trafficEntry, targetRevision string, desiredPercent int32) bool {
	for _, t := range traffic {
		if t.revision == targetRevision {
			return t.percent == desiredPercent
		}
	}
	// Revision not found in traffic — matches only if desired is 0%
	return desiredPercent == 0
}
