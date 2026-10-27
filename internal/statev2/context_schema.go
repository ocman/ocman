package statev2

// Restore current-request context from retained messages. Older compact
// projections kept only turn-wide token totals, which are not context sizes.
const contextTokensSchema = `
UPDATE analytics_message AS a
SET data=json_set(a.data,'$.contextTokens',COALESCE(
	json_extract(m.extensions,'$.metadata.contextTokens'),
	json_extract(m.extensions,'$.contextTokens')))
FROM message AS m
WHERE m.instance_id=a.instance_id AND m.session_id=a.session_id AND m.id=a.id
	AND COALESCE(json_extract(m.extensions,'$.metadata.contextTokens'),
		json_extract(m.extensions,'$.contextTokens'))>=0;
`
