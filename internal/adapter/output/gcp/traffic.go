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
