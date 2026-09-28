// Wire format of an artifact relay share (internal/server/artifact_share.go):
// chunk 0 is this manifest, later chunks are raw file bytes in order.
export type ArtifactShareManifest = {
  kind: 'artifact';
  title: string;
  description?: string;
  links: { url: string; label?: string }[];
  files: { name: string; mime: string; size: number; firstSeq: number; chunks: number }[];
};

export type ArtifactShareFile = { name: string; mime: string; size: number; blob: Blob };

export type ArtifactShare = {
  title: string;
  description?: string;
  links: ArtifactShareManifest['links'];
  files: ArtifactShareFile[];
};

/** Joins each file's decrypted chunks into a Blob; throws on a gap or size mismatch. */
export function assembleArtifactShare(manifest: ArtifactShareManifest, chunks: Map<number, Uint8Array>): ArtifactShare {
  const files = (manifest.files ?? []).map((f) => {
    const parts: Uint8Array[] = [];
    for (let i = 0; i < f.chunks; i += 1) {
      const part = chunks.get(f.firstSeq + i);
      if (!part) throw new Error(`artifact share is missing chunk ${f.firstSeq + i}`);
      parts.push(part);
    }
    const blob = new Blob(parts as BlobPart[], { type: f.mime });
    if (blob.size !== f.size) throw new Error(`artifact file ${f.name} is ${blob.size} bytes, expected ${f.size}`);
    return { name: f.name, mime: f.mime, size: f.size, blob };
  });
  return { title: manifest.title, description: manifest.description, links: manifest.links ?? [], files };
}
