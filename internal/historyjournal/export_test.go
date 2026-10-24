package historyjournal

// Read returns up to limit complete records after each segment's cursor, in
// segment-name order. A missing journal directory is an empty journal.
func Read(dir string, cursors map[string]int64, limit int) ([]Entry, error) {
	segments, err := Segments(dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, segment := range segments {
		if len(out) >= limit {
			break
		}
		entries, err := ReadSegment(dir, segment, cursors[segment], limit-len(out))
		if err != nil {
			return out, err
		}
		out = append(out, entries...)
	}
	return out, nil
}
