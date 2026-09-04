# Use human-addressable Factory graph identifiers

Factory graph nodes use a Work Epic goal's lowercase ASCII word-initial prefix (up to ten characters, with `epic` as fallback), a unique four-character cryptographic base32 suffix, and immutable monotonic child indices. A Work Epic is the root, its first Mol is `.1`, and every child appends its index to its parent. This replaces opaque root IDs and Formula-key child IDs so people can recognize, discuss, and navigate graph hierarchy without compromising uniqueness or audit stability.
