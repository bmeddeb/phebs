# T34.1 — repository source/search generation gates

> **Retained engineering gate.** This package replays T34.1's production
> source-generation contract against T32.3's public load profile and binds the
> selected direct topology to T32.2/T32.4's source-free target evidence. It is
> not a target SLO, an accuracy result, or multi-service release authority.

The neutral gate materializes the frozen 1,000-service profile, commits its
3,151 exact files, and runs the production streamed source census. The result
must contain 3,151 physical owners even though the authority contains 5,000
logical memberships. This proves the source/search build boundary does not
multiply repository bytes by service membership.

The target-derived gate does not reopen private inputs. It verifies that the
retained T32.4 direct-topology decision is cryptographically bound to the
completed T32.2 source-free result and still records one visible target shard,
zero restart children, and no cohort trigger. Those observations remain
environment-specific and do not set a production limit.

```sh
go test ./spike/t341 -count=1
```

