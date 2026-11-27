# Native acquisition cost gate (trial)

This package does not qualify or wire a native source. Before enabling one,
prove the number of physical upstream operations in the supported layout. For
one acquisition the conservative plan is:

```
C_total = C_binding + C_host + C_inventory + sum(C_sample[i], i = 1..N) <= 23
C_binding = connection/qualification attempts + version/info/identity requests
            + any later binding checks not already counted
C_host = 1 for one local open/read/close, or 0 when unsupported
C_inventory = list requests (all pages, including any terminal probe)
              + separate inventory/classification requests
C_sample[i] = stats request + any incarnation/classification checks for that sample
N <= 20; inventory candidates <= 100
```

Count each physical operation once. An inline dial plus its request is one
admitted operation only if fixed to that single exchange with no hidden extra
attempts; separate upstream work must be budgeted separately. A failed or
canceled-after-admission operation still counts. No retries, redials,
redirects, or hidden version negotiation are allowed. If exactly one operation
provides binding, one reads the host, one lists exhaustively, and each sample
costs one, the allocation is `1 + 1 + 1 + 20 = 23`. Incarnation evidence or
pagination can make that plan unaffordable. A capped page is not proof of
exhaustion, and client ceilings do not prove bounded server work.

Before starting aggregate work, either prove its worst case fits the remaining
call allowance or leave it unsupported. If independently affordable exhaustive
inventory is already available and reveals an unaffordable sample set, skip
all sample starts; a verified count may accompany a usable host pair. Never
attempt and swallow a real shared call/byte budget violation to keep host
metrics: a breach always makes the scrape unavailable. Unknown native cost
means unsupported, not a trial request or fabricated evidence. Every admitted
operation, including verification, must be gated before it starts and every
body metered before decoding. The trusted source seam cannot police I/O outside
these gates.

Cancellation prevents new admissions and reads once observed. An operation
already admitted is not refunded; cancellation does not interrupt a reader,
open, close, or binding cleanup that ignores its context. A cooperating reader
can return promptly and then cleanup runs synchronously. A noncooperating
reader holds admission until it returns; late data never qualifies for success.
Neither behavior establishes that dockerd canceled or bounded its own work.
