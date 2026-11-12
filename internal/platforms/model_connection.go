package platforms

// ModelForConnection validates a model against the selected account and its native namespace.
func ModelForConnection(connection, kind ID, ref string) (string, error) {
	if prefix, model := SplitModel(ref); prefix != "" && prefix == connection.Base() {
		return model, nil
	}
	return ModelFor(kind, ref)
}
