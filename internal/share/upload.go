package share

import "context"

// ChunkUploader seals and uploads the chunks of one relay share. It refuses
// a chunk the relay would reject for size before uploading it: one over the
// per-chunk limit, or one that takes this uploader's total past the
// per-share limit.
type ChunkUploader struct {
	Client RelayClient
	Alloc  RelayAllocation
	Key    Key
	sent   int64
}

// Put seals plain as chunk seq and uploads it.
func (u *ChunkUploader) Put(ctx context.Context, seq uint64, plain []byte) error {
	sealed, err := Seal(u.Key, u.Alloc.ID, seq, plain)
	if err != nil {
		return err
	}
	if u.Alloc.MaxChunkBytes > 0 && int64(len(sealed)) > u.Alloc.MaxChunkBytes {
		return TooLarge("chunk is too large after encryption")
	}
	if u.Alloc.MaxShareBytes > 0 && u.sent+int64(len(sealed)) > u.Alloc.MaxShareBytes {
		return TooLarge("share is too large")
	}
	u.sent += int64(len(sealed))
	return u.Client.Put(ctx, u.Alloc, seq, sealed)
}
