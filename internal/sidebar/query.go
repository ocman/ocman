package sidebar

// Only pending observations enter the recursive parent walk. Normal browsing
// never constructs the entire platform's parent map. All edges are source scoped.
// Background completion gates survive a foreground lease expiring, but never
// cross an adapter generation or borrow another source's task ownership.
const selectSessions = `WITH RECURSIVE runtime AS (
 SELECT json_extract(value,'$.Owner') AS owner,json_extract(value,'$.Platform') AS platform,json_extract(value,'$.Source') AS source,json_extract(value,'$.Generation') AS generation FROM json_each(?)
), requested AS (SELECT value AS id FROM json_each(?)), viewer AS (SELECT ? AS external), cutoffs AS (SELECT key AS id,value AS at FROM json_each(?)), connections AS (
 SELECT x.* FROM main.sidebar_connection x JOIN runtime r ON r.owner=x.owner_id AND r.platform=x.platform AND r.source=x.source_key AND r.generation=x.generation WHERE x.expires_at>?
), live AS (
 SELECT o.* FROM main.sidebar_observation o JOIN connections x
 ON x.owner_id=o.owner_id AND x.platform=o.platform AND x.source_key=o.source_key AND x.generation=o.generation
), prompts(id,parent_id,permission,question,owner_id,platform,source_key) AS (
 SELECT c.id,c.parent_native_id,o.pending_permission,o.pending_question,c.owner_id,c.platform,c.source_key FROM live o JOIN core.session_catalog c
 ON c.owner_id=o.owner_id AND c.platform=o.platform AND c.source_key=o.source_key AND c.native_id=o.native_id
 WHERE c.creation_state='ready' AND c.purged=0 AND (o.pending_permission=1 OR o.pending_question=1)
 UNION
 SELECT c.id,c.parent_native_id,p.permission,p.question,c.owner_id,c.platform,c.source_key FROM prompts p JOIN core.session_catalog c ON c.native_id=p.parent_id AND c.native_id<>'' AND c.owner_id=p.owner_id AND c.platform=p.platform AND c.source_key=p.source_key
 WHERE c.creation_state='ready' AND c.purged=0
 ), flags AS (SELECT id,max(permission) AS permission,max(question) AS question FROM prompts GROUP BY id),
 halted(id,parent_id,at,owner_id,platform,source_key) AS (
 SELECT c.id,c.parent_native_id,h.updated_at,c.owner_id,c.platform,c.source_key FROM core.session_catalog c
 JOIN runtime r ON r.owner=c.owner_id AND r.platform=c.platform AND r.source=c.source_key
 JOIN core.setting h ON h.key='session.halt.latest.'||c.id OR (c.legacy_keys=1 AND h.key='session.halt.latest.'||c.native_id)
 WHERE c.creation_state='ready' AND c.purged=0
 UNION SELECT c.id,c.parent_native_id,h.at,c.owner_id,c.platform,c.source_key FROM halted h
 JOIN core.session_catalog c ON c.native_id=h.parent_id AND c.native_id<>'' AND c.owner_id=h.owner_id AND c.platform=h.platform AND c.source_key=h.source_key
 WHERE c.creation_state='ready' AND c.purged=0
 ), halt_flags AS (SELECT id,max(at) AS at FROM halted GROUP BY id)
  SELECT s.id,i.platform,s.project_id,s.parent_id,s.title,s.directory,s.created_at,s.updated_at,s.last_message_at,
  COALESCE(att.last_turn_completed_at,0),COALESCE(att.last_user_prompt_at,0),COALESCE(hlt.at,0),
 COALESCE(b.branch,''),c.origin,c.origin_id,
 CASE WHEN c.origin='routine' THEN c.origin_id WHEN pc.origin='routine' THEN pc.origin_id ELSE '' END,
 CASE WHEN c.origin='factory' THEN c.origin_id WHEN pc.origin='factory' THEN pc.origin_id ELSE '' END,
 s.inferred_status,s.last_role,s.last_finish,s.last_error,COALESCE(o.status,''),COALESCE(o.resets_at,0),x.source_key IS NOT NULL,
 COALESCE(f.permission,0),COALESCE(f.question,0),COALESCE(bg.background_tasks,0),COALESCE(bg.background_services,0),COALESCE(pin.pinned_at,0),a.session_id IS NOT NULL,
 COALESCE(a.session_time_updated,0),COALESCE(a.snoozed_until,0),COALESCE(ap.archived_at,0),
 CASE WHEN viewer.external THEN COALESCE(h.at,0) ELSE COALESCE(v.session_time_updated,0) END,v.session_id IS NOT NULL,COALESCE(v.interrupted,0),COALESCE((SELECT value FROM core.setting WHERE key='session.archive_resurface'),'halt'),
 CASE WHEN (CASE WHEN viewer.external THEN COALESCE(h.at,0) ELSE COALESCE(v.session_time_updated,0) END)>=s.updated_at THEN 0 ELSE
 (SELECT count(*) FROM main.message m WHERE m.instance_id=s.instance_id AND m.session_id=s.id AND m.source_deleted=0 AND m.created_at>(CASE WHEN viewer.external THEN COALESCE(h.at,0) ELSE COALESCE(v.session_time_updated,0) END)) END
 FROM main.session_sidebar s JOIN main.instance i ON i.id=s.instance_id
 JOIN core.session_catalog c ON c.id=s.id AND c.owner_id=i.owner_id AND c.platform=i.platform AND c.source_key=i.source_key
 LEFT JOIN core.session_catalog pc ON pc.id=c.parent_id AND pc.owner_id=c.owner_id AND pc.platform=c.platform AND pc.source_key=c.source_key
 LEFT JOIN live o ON o.native_id=c.native_id AND o.source_key=c.source_key AND o.platform=c.platform
 LEFT JOIN connections x ON x.owner_id=i.owner_id AND x.platform=i.platform AND x.source_key=i.source_key
  LEFT JOIN flags f ON f.id=s.id
  LEFT JOIN main.sidebar_observation bg ON bg.native_id=c.native_id AND bg.owner_id=c.owner_id AND bg.platform=c.platform AND bg.source_key=c.source_key AND bg.generation=(SELECT generation FROM runtime WHERE owner=c.owner_id AND platform=c.platform AND source=c.source_key)
  LEFT JOIN halt_flags hlt ON hlt.id=s.id
  LEFT JOIN main.sidebar_attention att ON att.instance_id=s.instance_id AND att.session_id=s.id
 LEFT JOIN main.sidebar_branch b ON b.owner_id=i.owner_id AND b.directory=s.directory
 LEFT JOIN core.pinned_session pin ON pin.platform=i.platform AND pin.session_id=s.id
 LEFT JOIN core.archived_session a ON a.platform=i.platform AND a.session_id=s.id
 LEFT JOIN core.archived_project ap ON ap.remote_id='local' AND ap.project_root=COALESCE(NULLIF(s.project_directory,''),s.directory)
 LEFT JOIN core.seen_session v ON v.platform=i.platform AND v.session_id=s.id
 LEFT JOIN cutoffs h ON h.id=s.id CROSS JOIN viewer
 WHERE i.owner_id=? AND i.platform IN (SELECT platform FROM runtime) AND c.creation_state='ready' AND c.purged=0
 AND (s.source_deleted=0 OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested)
 AND (s.is_subagent=0 OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested)
 AND (s.source_child=0 OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested OR (o.status='busy' AND COALESCE(f.permission,0)=0 AND COALESCE(f.question,0)=0))
 AND (?='' OR s.directory=?)
 AND (?=0 OR s.updated_at>=? OR pin.session_id IS NOT NULL OR COALESCE(a.snoozed_until,0)>0 OR s.id IN requested OR COALESCE(f.permission,0)=1 OR COALESCE(f.question,0)=1)
 AND (?='' OR c.origin=?) AND (?='' OR c.origin_id=?)
 ORDER BY s.updated_at DESC,s.id LIMIT ? OFFSET ?`
