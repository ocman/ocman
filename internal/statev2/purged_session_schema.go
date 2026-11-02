package statev2

// retirePurgedSessionSchema drops a table no release ever wrote: statev2 has
// no purge operation. change_outbox keeps its 'purged' kind check because
// rebuilding the outbox would rewrite every commit row for no benefit.
const retirePurgedSessionSchema = `DROP TABLE IF EXISTS purged_session;`
