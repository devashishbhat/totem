# Golden event sequences

Each file is one scenario: a list of events as they would arrive from the SDK,
and the state the lineage worker must reach after processing them. They are the
record of what Totem's lineage rules mean, so a change to the worker has to say
which scenario it changes.

Every event here is timestamped well in the past, which puts it past the
worker's grace window. A reference that cannot be resolved is therefore skipped
immediately rather than deferred; the deferral path itself is tested in
`worker_test.go`, which controls the clock.

The tests need a Postgres to create databases on:

    TM_TEST_DATABASE_URL=postgres://totem:totem@localhost:5432/postgres go test ./internal/lineage/
