# How the technical roadmap is worked out

English | [中文](planning.zh-CN.md)

This document records how pingmock's technical roadmap is planned, and the evidence and sources the round of October 2026 used. The aim is that the next round can follow the same steps, and that every direction can be traced back to what it rests on. The roadmap itself is in [TODO.md](../TODO.md#技术路线) (in Chinese).

## Principles

- **Four core values, for every protocol.** Everything pingmock does has to be **transparent** (the program under test needs no change to its code or configuration), **per target** (every target can have its own conditions), **reproducible** (the same setup gives the same results every time) and **controllable and observable** (conditions can change during a test, and what the program did can be seen afterwards). Every protocol pingmock comes to support has to keep all four; **a design for a new protocol that cannot keep one of them must be reconsidered.** Definitions and examples are in [CONTRIBUTING](../CONTRIBUTING.md#design-principles).
- **Test applications, not protocols.** The phrase comes from Toxiproxy's maintainers. pingmock helps people test programs that depend on ping, not study ICMP itself. A feature that does not help users test their own programs waits.
- **Evidence first.** Every direction needs evidence that can be traced: an issue, an experiment, a compatibility test result. A direction without any waits, however useful it sounds.
- **Verify the premises before designing; agree on the syntax before implementing.** Technical premises are checked by experiment. Syntax users will write into configuration files gets a design proposal agreed with the maintainer first, since it is hard to change once released.
- **Order by number of users.** Serve the largest group of users first, then widen to narrower ones.

## Method

Seven steps: the first five gather evidence, the sixth verifies, the seventh puts it together.

### 1. Compatibility testing: find the gaps with real tools

Run common ping tools and libraries under pingmock and see what works. This is the most direct evidence, since it depends on nobody's word.

- What to test: the system's `ping`, a bulk ping tool, the most used ping library of each major language, and neighboring tools (traceroute, mtr)
- Test libraries both ways: with raw sockets, which need root, and with unprivileged ping sockets, which the kernel handles differently
- What is settled goes into CI: [`test/e2e-compat.sh`](../test/e2e-compat.sh)

### 2. Gather what people ask for

Search for what people ask and complain about, from several angles:

- how to do X ("how to add latency to only one address", "how to test code that pings")
- the pain points of existing tools
- neighboring needs (faking traceroute paths, mocking by hostname)

Ask each time: **is this really about ping?** People who want "latency to one address only" usually want it for all traffic, which is the domain of traffic shaping tools.

### 3. Find reference projects, and tell the technical routes apart

Find projects that do similar things, and first decide whether their **technical route** is pingmock's:

- **The same route** (TUN devices, network namespaces): their implementation details are worth studying
- **A different route** (Toxiproxy is a TCP proxy, say): their internals mostly do not apply; borrow only at the product level (API, clients, documentation)

### 4. Read the reference projects' code

Read with specific questions: how does it build a given reply? How does it handle a given problem? Note its bugs too, to avoid repeating them (fakert quotes the whole original packet, without a size limit).

### 5. Mine the reference projects' issues

This step is often the most valuable:

- sort by reactions to see what users want most
- filter by keywords (test, simulat, dns, ttl…) for discussions relevant to pingmock
- **open issues filed by the project's own author** are the most telling: the author wants the feature (Trippy #1552, #1554)
- look at how the project **tests itself**: pro-bing's tests depend on `www.google.com`, which shows it lacks a controllable network

### 6. Verify the premises by experiment

Evidence and ideas both get checked by experiment:

- verify feasibility with the smallest prototype (a few dozen lines of Python forging a path on a TUN device, to see whether traceroute shows it)
- test both sides: success where success is expected, and the conditions that make it fail (with strict reverse path filtering, it fails unless routes are added)
- make sure the experiment **really ran**: when the setup failed (a missing kernel module, a syntax error in a command), the result is false and is no conclusion

### 7. Put it together into a roadmap

- group the evidence by **user**, to see what each group needs
- order by number of users into **releases that each ship and bring value on their own**
- record the directions **not taken** and why, so they are not argued again

## Tools and techniques

**The GitHub API** (`curl` and `python3` are enough):

| To find out | Endpoint |
|---|---|
| A project's stars and activity | `GET /repos/{owner}/{repo}` |
| All issues, then sort by reactions and filter by keyword locally | `GET /repos/{owner}/{repo}/issues?state=all&per_page=100&page=N` (pull requests are included; tell them apart by the `pull_request` field) |
| An issue's text and comments | `GET /repos/{owner}/{repo}/issues/{n}` and `.../comments` |
| Source code | `GET /repos/{owner}/{repo}/contents/{path}` with the header `Accept: application/vnd.github.raw` |
| The directory tree | `GET /repos/{owner}/{repo}/git/trees/{branch}?recursive=1` |
| The first release that contains a fix | `GET /repos/{owner}/{repo}/compare/{commit}...{tag}`: a `status` of `ahead` or `identical` means the release contains it |

Note: a fine-grained token granted for one's own repository can read public repositories but **cannot use the search API**; without a token, the search API allows 10 calls a minute.

**Experiments**:

- network and mount namespaces: `unshare --user --map-root-user --net --mount`, which an unprivileged user can run
- a TUN prototype: see the Python script in the appendix of [Inside the packets](packets.md#appendix-capture-packets-yourself)
- background processes in a local simulation are not cleaned up as they are in a container when process 1 exits; stop them yourself

**Engaging with communities**: Hacker News, Reddit's r/golang and V2EX forbid AI-generated posts and replies. Introducing pingmock in other projects' issues and taking part in discussions should be done by the author; AI can prepare material and check facts.

## The evidence of October 2026

### Compatibility testing

| Tool / library | Result |
|---|---|
| The system's `ping` (iputils), fping, Go's pro-bing, Python's icmplib (raw and ping sockets) | Work: delays, deterministic loss, unreachable and a hijacked real address all as the rules say |
| traceroute (default UDP mode) | `*` at every hop, never reaching the target: pingmock does not answer UDP probes |
| traceroute -I, mtr | A single hop: pingmock ignores the TTL |
| `ping www.baidu.com` (experiment) | Editing `/etc/hosts` works as root; under `exec`, the name does not resolve |

### What people ask for

A small sample without numbers.

| Need | Evidence | Conclusion |
|---|---|---|
| Latency to one destination only | Almost always `tc netem`, and easy to get wrong: delaying one address delays all traffic; without filters one's own SSH slows down | Usually for all traffic: traffic shaping, which pingmock does not do |
| Testing code that pings | Hawkular's Pinger tests ping real websites and fail when one is down; someone patching `icmplib.async_ping` with pytest-mock got the patch target wrong | pingmock's core, already solved |
| Testing network diagnostic tools | Trippy #759: the most important integration test would simulate a network on a TUN device answering ICMP, UDP and TCP probes | Needs path simulation |

### Reference projects

| Project | Route | Learned |
|---|---|---|
| [Toxiproxy](https://github.com/Shopify/toxiproxy) (12,000 stars) | Different: a TCP proxy | Borrow `POST /reset`, `GET /version`, clients in many languages, a README that says what it is for. Its maintainers say in [#39](https://github.com/Shopify/toxiproxy/issues/39) that a layer 3 proxy would mean TUN devices and a rewrite, and they value portability, so contributing ping support to it is not viable |
| [Trippy](https://github.com/fujiapple852/trippy)'s simulation test | The same: TUN | Its path model is nearly pingmock's design (a list of hops by TTL, each replying or silent); at the target, ICMP gets an echo reply, UDP a port unreachable, and **TCP just a SYN-ACK**; its 28 scenario files make an acceptance checklist |
| [fakert](https://github.com/sehaas/fakert), [traceroute-haiku](https://github.com/benjojo/traceroute-haiku) | The same: TUN and routes | IPv6 time exceeded is ICMPv6 type 3 code 0, port unreachable type 1 code 4; fakert quotes the whole original packet without a limit, to be avoided; traceroute-haiku derives the hops by rule, and their names come from reverse DNS |
| [slirp4netns](https://github.com/rootless-containers/slirp4netns), pasta | The same: namespaces | An unprivileged user can bind-mount over `/etc/resolv.conf` in user and mount namespaces (**verified with `/etc/hosts`**: glibc, Go and Python all read it, and the host is untouched); a built-in DNS forwarder; outbound access can come from them rather than be built |

### The reference projects' issues

| Issue | Content | Meaning for pingmock |
|---|---|---|
| [Trippy #1552](https://github.com/fujiapple852/trippy/issues/1552) (by its author, open) | Extract the simulator into a standalone command-line tool | Confirms the direction of path simulation |
| [Trippy #1554](https://github.com/fujiapple852/trippy/issues/1554) (by its author, open) | Loss at one hop must also drop the later hops in the same round | Path simulation must tell **link loss** (affecting every later hop) from **a hop that does not answer** (ICMP rate limiting, affecting that hop alone) |
| slirp4netns #333, #225, #116, #347, #312, #280 | `/etc/resolv.conf` as a symlink; forwarding to the first DNS server only; no search domains; the default network overlapping the user's | The checklist for hostname support |
| Toxiproxy #281, #447, #274, #273 | Most wanted: faults declared in the configuration file, a custom random seed, faults by subnet | pingmock has all of them; the note on when to use pingmock should say so |
| [pro-bing #70](https://github.com/prometheus-community/pro-bing/issues/70), #111, #116, #27, go-ping #192 | No error on timeout, and `Run()` blocking on an unreachable host; unit tests pinging real websites concurrently; recording and setting the TTL | Ping library developers are a new group of users; custom reply TTLs have evidence of need |

### Experiments

| Experiment | Conclusion |
|---|---|
| A Python prototype forging two hops on a TUN device | traceroute (ICMP and UDP) and mtr show them; pingmock sees the original TTL |
| Strict reverse path filtering (`rp_filter=1`) | Without routes for the hops' addresses the kernel drops their replies; with routes it works |
| An unprivileged user bind-mounting over `/etc/hosts` in namespaces | Works: every language's resolver reads the new file, and the host is untouched |
| Two iputils versions receiving an echo reply with a wrong checksum | 20240117 says nothing; from 20240905 it marks `(BAD CHECKSUM!)` (commit bacf1b7) |

## A second round, 20 October 2026

Done by the [`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent, as market research only; every row below was then opened and checked by hand. It also checked claims about operations staff that the roadmap had taken from an earlier conversation without sources.

### Claims checked

| Claim | Verdict | Evidence |
|---|---|---|
| Zabbix users can only test ping alerts by lowering thresholds | **Refuted** | Zabbix has a built-in way to test trigger expressions, and its own ticket [ZBX-19712](https://support.zabbix.com/browse/ZBX-19712) reproduces a host going down with "Simulate "host down" situation (power off host, or add block firewall rule)" |
| Uptime Kuma had a bug that pinged every second | **Not found** | The only source, [#4837](https://github.com/louislam/uptime-kuma/issues/4837) "Uptime-Kuma causing ping floods", was disputed by a collaborator ("In the timeframe, of 190s, that is reasonable") and closed by the stale bot on 2024-08-26, without a fix |
| "Seven technical routes" for faking network answers | **No source** | No document counts them. The projects found group into seven or eight routes, one of them **LD_PRELOAD interception** of socket calls, which this document did not list |
| Pinging with a bound interface matters for operations tools | **Confirmed, with a correction** | Current mwan3, OpenWrt's multi-WAN failover, no longer runs `ping -I`: [mwan3track](https://raw.githubusercontent.com/openwrt/packages/master/net/mwan3/files/usr/sbin/mwan3track) runs `WRAP $PING -n -c $count ...`, where `WRAP` sets `DEVICE=$DEVICE SRCIP=$SRC_IP FWMARK=... LD_PRELOAD=/lib/mwan3/libwrap_mwan3_sockopt.so.1.0`, and [sockopt_wrap.c](https://raw.githubusercontent.com/openwrt/packages/master/net/mwan3/src/sockopt_wrap.c) "intercepts syscalls to various networking functions" to set `SO_BINDTODEVICE` and `SO_MARK`. Probes bound to a real device bypass pingmock's routes, so mwan3 is probably not mockable today (to verify by experiment) |

### The reference projects' issues, again

| Issue | Change | Meaning for pingmock |
|---|---|---|
| [Toxiproxy #739](https://github.com/Shopify/toxiproxy/pull/739) | Merged 2026-06-29: "feat: add packet_loss toxic for simulating flaky network conditions" | Toxiproxy now has loss too, for TCP |
| [Toxiproxy #447](https://github.com/Shopify/toxiproxy/issues/447) | 2026-10-14: a contributor proposes "an optional toxics array to each proxy in the existing config file"; a contributor (not a core maintainer) welcomes it | Faults declared in a configuration file may stop being a difference from Toxiproxy |
| [Toxiproxy #610](https://github.com/Shopify/toxiproxy/pull/610) | Open since 2024-12-27: "Added support for setting seed for deterministic results." | Same for a seed. The comparison in the README should be checked again when either lands |
| [pro-bing #195](https://github.com/prometheus-community/pro-bing/pull/195) | "Add ICMP time exceeded support", "useful when you want to catch ICMP time exceeded messages, in order to implement traceroute-like tools"; changes requested by a maintainer on 2026-10-11 | Path simulation can test it without real routers: an opening for user ② |
| [Trippy #1551](https://github.com/fujiapple852/trippy/pull/1551) | Draft "WIP: Extract simulator to trippy-sim crate", "Closes #1552", last updated 2025-12-07 | #1552 is being worked on in Trippy itself: a future alternative for user ④ |
| [slirp4netns #361](https://github.com/rootless-containers/slirp4netns/issues/361) | By its author, 2026-04-07: "I'm considering deprecating slirp4netns in favor of it [gvisor-tap-vsock]" | Outbound access for `exec` should look at pasta and gvisor-tap-vsock rather than slirp4netns |
| Trippy #1552, #1554, pro-bing #70 | No change | |

pingmock itself had no issues or pull requests yet. Discussions were not checked (they need an authenticated API).

## A third round: multi-protocol, 20 October 2026

The [`pingmock-planning`](../.claude/agents/pingmock-planning.md) agent did market research, technical selection and a design proposal for the next protocol. Its recommendation, TCP connection behavior before DNS faults, was adopted. The rows below were checked by hand; the rest of its report is agent-reported and was not checked.

| Evidence | Source | Meaning |
|---|---|---|
| CPython keeps a host to test connect timeouts | [`Lib/test/test_timeout.py`](https://raw.githubusercontent.com/python/cpython/main/Lib/test/test_timeout.py): `blackhole = resolve_address('pythontest.net', 56666)`, "Blackhole has been configured to silently drop any incoming packets." | Projects build and maintain their own TCP black holes |
| LiteLLM points its timeout tests at an unroutable address | [PR #39266](https://github.com/BerriAI/litellm/pull/39266), open since 2026-09-02: "test(local_testing): point the sub-ms timeout tests at an unroutable api_base" | The workaround depends on the network the tests run on |
| asyncpg's multi-host failover and timeouts | [#1155](https://github.com/MagicStack/asyncpg/issues/1155), open since 2024-05-18: "Timeout configuration for multi-host config" | Failover between hosts needs one host that times out and one that answers |
| What pingmock already answers to TCP | Experiment, rerun by hand in the development container: with a path and a rule of `200ms`, a Python `connect` succeeds in 200.7ms and a read then times out; `timeout` makes the connect time out; `unreachable` fails at once with `No route to host` | The same mechanism, extended with refusals and per-port rules, covers connect behavior in both modes |

Against: those projects fixed their problems cheaply, with unroutable addresses or iptables rules, and Kubernetes probes run outside `pingmock exec`. For DNS faults the agent found no developer asking for them, and chaos tools already offer them.

## Next time

1. **Start from users' feedback**: real needs in [Discussions](https://github.com/nanmuyao/pingmock/discussions) and issues come before any research
2. Rerun the compatibility tests with new versions of the tools and libraries, adding new common tools
3. Check the issues cited this round for progress (Trippy #1552, #1554, pro-bing #70, Toxiproxy #447)
4. Repeat the seven steps above and add the new evidence to this document

The Claude Code agent [`pingmock-planning`](../.claude/agents/pingmock-planning.md) can take on three kinds of work in this method: market research (steps 2, 3 and 5, the progress of cited issues and users' feedback), technical selection (comparing the options for a question against the four core values, with experiments in the [development container](../.devcontainer/README.md)), and project design (a written proposal for a roadmap item, with the syntax users would write and the open questions). Every claim it makes cites a source, an experiment or a line of code. It writes reports and proposals without changing anything; its findings are checked before they go into this document, and its designs are decided by the maintainer.
