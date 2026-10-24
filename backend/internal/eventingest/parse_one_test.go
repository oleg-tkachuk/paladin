package eventingest

import "fmt"

// parseOne is the single event src parses raw into, for a message that
// carries one.
func parseOne(src Source, raw []byte, contentType string) (CloudEvent, error) {
	evs, err := src.Parse(raw, contentType)
	if err != nil {
		return CloudEvent{}, err
	}
	if len(evs) != 1 {
		return CloudEvent{}, fmt.Errorf("parsed %d events, want one", len(evs))
	}
	return evs[0], nil
}
