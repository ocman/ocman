# Keep the Factory graph mutable

Users may create, edit, reparent, link, unlink, and soft-delete unstarted Factory graph work through typed REST and authenticated MCP actions, including local cross-Work Epic dependencies. Mutations globally reject cycles; running and closed work remains structurally immutable, while soft-deletion retains audit history and removes the Issue and its incident edges from the active graph atomically. Plan materialization remains a graph-generation path, not the only graph-write authority.
