No optimisations
══════════════════════════════════════════════════════════════════════
Phase                  Requests   Elapsed     Req/s       p50       p95       p99
──────────────────────────────────────────────────────────────────────
Write (seq)                 500     2.97s     168.5     0.3ms     0.4ms     0.5ms
Read (seq)                  500     3.17s     157.9     0.4ms     0.6ms     0.8ms
Read (10w)                 1000     1.22s     818.9     0.8ms     1.5ms     1.7ms
Mixed (10w)                1000     1.20s     830.3     0.6ms     1.2ms     1.5ms
Edge ops                    100     5.05s      19.8     0.7ms    92.8ms    97.5ms


1st round of optimisation
══════════════════════════════════════════════════════════════════════
Phase                  Requests   Elapsed     Req/s       p50       p95       p99
──────────────────────────────────────────────────────────────────────
Write (seq)                 500     2.83s     176.6     0.2ms     0.3ms     0.4ms
Read (seq)                  500     3.03s     165.2     0.4ms     0.5ms     0.6ms
Read (10w)                 1000     1.22s     816.8     0.8ms     1.4ms     1.7ms
Mixed (10w)                1000     1.15s     869.8     0.5ms     0.9ms     1.1ms
Edge ops                    100     0.61s     164.7     0.4ms     0.6ms     0.7ms


2nd round of optimisation
══════════════════════════════════════════════════════════════════════
Phase                  Requests   Elapsed     Req/s       p50       p95       p99
──────────────────────────────────────────────────────────────────────
Write (seq)                 500     3.26s     153.4     0.3ms     0.4ms     0.4ms
Write (10w)                 500     0.62s     810.0     0.4ms     0.6ms     0.8ms
Read (seq)                  500     3.62s     138.0     0.5ms     0.7ms     0.9ms
Read (10w)                 1000     1.28s     778.8     1.2ms     1.9ms     2.3ms
Mixed (10w)                1000     1.30s     769.1     0.8ms     1.5ms     1.8ms
Edge ops                    100     0.76s     130.8     0.6ms     0.8ms     1.4ms