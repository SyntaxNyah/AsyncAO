# PGO capture session script

Goal: a CPU profile representative of real courtroom load for `default.pgo`.

1. Build a release binary and start it with profiling:
   `./asyncao -debug`
2. Join a populated server (or a local one with bots), pick a character.
3. For ~5 minutes, continuously:
   - send IC messages with preanims and pair with another character,
   - swap emotes every few messages (sprite churn → decode + upload),
   - change areas twice (background reload + epoch cancellation),
   - play music once, trigger each shout once.
4. While that runs: `curl -o default.pgo "http://localhost:6060/debug/pprof/profile?seconds=120"`
5. Replace `default.pgo` at the repo root, rebuild with `-pgo=auto`,
   sanity-check the benchmarks, commit.
