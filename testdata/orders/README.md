# Fixture corpus

Captured reconciliations, replayed offline by `TestCorpus` in `app/`.

Each file is one purchase order: both built orders with their embeddings, the
run record, the thresholds, and the result that run produced. Replaying needs
no Business Central, no SharePoint, no Document Intelligence, no embeddings
key and no credentials.

## Filling it

Run the service with capture on:

    CAPTURE_FIXTURES=true

Every successful reconciliation is written here as `<PF>_<timestamp>.json`.
Nothing is overwritten: a fixture is evidence of how the engine behaved on a
real order, and a later run gets its own file.

## Using it

    go test ./app/ -run TestCorpus -v

`TestCorpus` replays every fixture and asserts the same pairings, the same
check verdicts and the same discrepancies. `TestCorpus_ThresholdSweep` prints
the flag rate at other thresholds without asserting anything, so calibration
is a test run rather than a batch against live services.

## Regenerating expectations

    go test ./app/ -run TestCorpus -update

Replays each fixture and writes the new result back into it, leaving the
captured inputs untouched — only `expected` changes. It re-encodes exactly as
the capture path does, so a fixture whose verdicts did not move comes back
byte-identical and the diff shows precisely what did.

Use it only for a behaviour change you intended and have measured. A fixture
is evidence of how the engine behaved on a real order; regenerating without
first reading what moved turns the corpus into a mirror.
