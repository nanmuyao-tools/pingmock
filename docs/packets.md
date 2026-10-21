# Inside the packets

English | [中文](packets.zh-CN.md)

This document takes a ping request apart, and in section 12 a TCP connection attempt: what each byte means, how pingmock decides that a packet is a ping request, how checksums are computed, and a few details worth knowing, such as where the `0x4000` and `0x3fff` in the flags and fragment field come from. Every byte shown comes from a **real, captured** ping request, and every conclusion was checked by experiment.

For how the request gets from `ping` to pingmock, see [The round trip of one ping](packet-path.md); for how pingmock handles it inside, see the [code tour](architecture.md).

If addresses, packets, TCP or DNS are new to you, read [Network basics](basics.md) first: it explains them with diagrams.

## 1. A real ping request

After `ping 10.98.0.1`, the request read from the TUN device is 84 bytes long (16 bytes per line, with the decimal offset on the left):

```
0000  45 00 00 54 4c 17 40 00 40 01 d8 cf 0a 62 00 fe
0016  0a 62 00 01 08 00 61 35 05 d7 00 01 86 35 d4 6a
0032  00 00 00 00 74 7f 03 00 00 00 00 00 10 11 12 13
0048  14 15 16 17 18 19 1a 1b 1c 1d 1e 1f 20 21 22 23
0064  24 25 26 27 28 29 2a 2b 2c 2d 2e 2f 30 31 32 33
0080  34 35 36 37
```

It has three layers:

```
┌──────────── IP header (20 bytes) ──────────┐┌─ ICMP header (8 bytes) ─┐┌───── data (56 bytes) ─────┐
 version length ... TTL protocol checksum src dst   type code checksum id seq   time of sending + padding
```

- The **IP header** is the envelope: sender, recipient and what it contains
- The **ICMP header** says what kind of message this is, with the identifier and sequence number `ping` uses to recognize its own replies
- The **data** starts with the time `ping` sent the request, followed by padding

## 2. The IP header: bytes 0 to 19

| Offset | Bytes | Field | Meaning |
|---|---|---|---|
| 0 | `45` | version + header length | High 4 bits = 4 (IPv4); low 4 bits = 5, a header of 5 × 4 = 20 bytes |
| 1 | `00` | type of service | 0, normal priority |
| 2-3 | `00 54` | total length | 84 bytes, the whole packet including the IP header |
| 4-5 | `4c 17` | identification | 19479, which tells the fragments of one packet apart from others |
| 6-7 | `40 00` | flags + fragment offset | `0x4000`: "don't fragment", offset 0 (see [section 6](#6-flags-and-fragment-offset-where-0x4000-0x3fff-and-0x00b9-come-from)) |
| 8 | `40` | TTL | 64; each router decrements it, and the packet is dropped at 0 |
| 9 | `01` | protocol | 1, the packet carries ICMP (6 would be TCP, 17 UDP) |
| 10-11 | `d8 cf` | header checksum | `0xd8cf`, to detect a damaged IP header (see [section 8](#8-how-checksums-are-computed)) |
| 12-15 | `0a 62 00 fe` | source address | 10.98.0.254, the side sending the ping |
| 16-19 | `0a 62 00 01` | destination address | 10.98.0.1, the side being pinged |

## 3. The ICMP header and data: bytes 20 to 83

| Offset | Bytes | Field | Meaning |
|---|---|---|---|
| 20 | `08` | type | 8 = Echo Request; a reply is 0 |
| 21 | `00` | code | 0, the only code echo messages use |
| 22-23 | `61 35` | ICMP checksum | `0x6135`, to detect damage to the ICMP part (header and data) |
| 24-25 | `05 d7` | identifier | 1495, which `ping` uses to recognize its own replies |
| 26-27 | `00 01` | sequence number | 1, which request this is |
| 28-35 | `86 35 d4 6a 00 00 00 00` | time of sending, seconds | 1792292230 seconds since 1970 |
| 36-43 | `74 7f 03 00 00 00 00 00` | time of sending, microseconds | 229236 |
| 44-83 | `10 11 12 … 37` | padding | counting up from `0x10` to make 56 bytes of data |

The time converts to 2026-10-18 02:57:10.229236, the moment this ping was sent. The two time fields are **little-endian** (low byte first) because `ping` copies them straight from memory and only `ping` reads them, while the numbers in the IP and ICMP headers are **big-endian** (high byte first), as network protocols specify.

## 4. Quick reference: hexadecimal and bit operations

Code that reads packets uses hexadecimal and bit operations everywhere.

**Each hexadecimal digit stands for exactly 4 bits**, so hexadecimal shows every bit directly:

| Hex | Binary | | Hex | Binary |
|---|---|---|---|---|
| `0` | 0000 | | `8` | 1000 |
| `1` | 0001 | | `9` | 1001 |
| `2` | 0010 | | `b` | 1011 |
| `3` | 0011 | | `e` | 1110 |
| `4` | 0100 | | `f` | 1111 |

For example, `0x3fff` = `3 f f f` = `0011 1111 1111 1111`, much clearer than decimal 16383.

**Three common operations:**

| Code | Name | What it does | Example |
|---|---|---|---|
| `x >> 4` | right shift | drops the low 4 bits, keeping the high ones | `0x45 >> 4` = 4 |
| `x & m` | bitwise AND | 1 only where both bits are 1: **keeps just the bits set in the mask `m`** | `0x45 & 0x0f` = 5 |
| `binary.BigEndian.Uint16(b)` | big-endian read | joins 2 bytes into a 16-bit number, the first byte high | `40 00` → `0x4000` |

## 5. How pingmock recognizes a ping request

Not everything on the TUN device is a ping request (TCP connection attempts are routed there too), so pingmock first checks that a packet is a ping request it can answer. The code is in [`internal/icmp/icmp.go`](../internal/icmp/icmp.go).

### 5.1 The version first: `ParseEchoRequest`

```go
switch pkt[0] >> 4 {
case 4:
    return parse4(pkt)   // IPv4
case 6:
    return parse6(pkt)   // IPv6
}
return EchoRequest{}, false
```

The high 4 bits of byte 0 are the version. Shifting right by 4 drops the low 4 bits (the header length):

```
pkt[0]      = 0x45 = 0100 0101
                     version  header length
pkt[0] >> 4 =        0000 0100 = 4  → IPv4, handled by parse4
```

An IPv6 packet starts with `0x60`, which gives 6 and goes to `parse6`. Anything else is not a ping request.

### 5.2 Then one check after another: `parse4`

| Check | Where in the packet | In the example | Why |
|---|---|---|---|
| Long enough for an IP header | the whole packet | 84 ≥ 20 | a shorter packet does not even hold an IP header |
| Length of the IP header | byte 0 `& 0x0f`, × 4 | 5 × 4 = 20 | the IP header **may carry options**, so this says where ICMP starts |
| Plausible total length | bytes 2-3 | 84 | keeps a malformed packet from sending the code to wrong offsets |
| Not a fragment | bytes 6-7 `& 0x3fff` | 0 | pingmock does not reassemble fragments; it handles whole packets only (see section 6) |
| Carries ICMP | byte 9 | 1 | rules out TCP, UDP and other protocols |
| Is an echo request | ICMP bytes 0 and 1 | type 8, code 0 | ICMP also has replies, unreachable errors and more |

When every check passes, the source, destination, identifier and sequence number go on to the decision.

## 6. Flags and fragment offset: where `0x4000`, `0x3fff` and `0x00b9` come from

This section derives the numbers in the fragment check `pkt[6:8] & 0x3fff != 0` and checks them against real fragments.

### 6.1 The layout of these 16 bits

RFC 791 divides the 16 bits of bytes 6-7 into three flag bits and a 13-bit fragment offset:

```
bit:   15     14       13        12 11 10  9  8  7  6  5  4  3  2  1  0
     reserved  don't   more     ─────────── fragment offset (13 bits) ───────────
              fragment fragments
```

Each part on its own:

| Bits | Meaning | Value |
|---|---|---|
| bit 15 | reserved, must be 0 | `0x8000` = 1000 0000 0000 0000 |
| bit 14 | don't fragment (DF) | `0x4000` = 0100 0000 0000 0000 |
| bit 13 | more fragments (MF) | `0x2000` = 0010 0000 0000 0000 |
| bits 0 to 12 | fragment offset, all set | `0x1fff` = 0001 1111 1111 1111 |

### 6.2 Where the three numbers come from

**`0x4000`** has only bit 14, "don't fragment", set. It is the actual value in the example request: the packet is small, and Linux sets "don't fragment" on such packets for path MTU discovery.

**`0x3fff`** is "more fragments" plus every offset bit: `0x2000 + 0x1fff = 0x3fff`. It is a **mask**: whether a packet is a fragment depends only on these 14 bits, not on "reserved" or "don't fragment", which is why those two bits are 0 in the mask.

**`0x00b9`** is the value in the second fragment when a packet is split in two, and it can be worked out:

1. The device carries at most 1500 bytes at a time (its MTU); minus the 20-byte IP header, each fragment holds up to 1480 bytes of data
2. So the second fragment's data starts at byte 1480 of the original
3. The fragment offset **counts in units of 8 bytes**: 1480 ÷ 8 = 185, which is `0xb9`

The offset counts in 8-byte units because it has only 13 bits, enough for 8191; times 8, that reaches 65528 and covers the largest possible IP packet, 65535 bytes.

### 6.3 Applying the mask

`& 0x3fff` clears "reserved" and "don't fragment" and keeps the other 14 bits. A result of 0 means "no more fragments" and "offset 0", that is, **a whole packet that was not split**; anything else is a fragment:

| Bytes 6-7 | Binary | `& 0x3fff` | Conclusion |
|---|---|---|---|
| `0x4000` (the example request) | 0100 0000 0000 0000 | `0x0000` | a whole packet: handled |
| `0x0000` (no flags at all) | 0000 0000 0000 0000 | `0x0000` | a whole packet: handled |
| `0x2000` (first fragment) | 0010 0000 0000 0000 | `0x2000` | a fragment: ignored |
| `0x00b9` (second fragment) | 0000 0000 1011 1001 | `0x00b9` | a fragment: ignored |
| `0x6000` (don't fragment + more fragments) | 0110 0000 0000 0000 | `0x2000` | a fragment: ignored |

### 6.4 Checked against real fragments

`ping -s 2000 10.96.0.1` sends 2000 bytes of data, more than the device carries at once, so the kernel splits it in two. Captured:

| | Length | Bytes 6-7 | Don't fragment | More fragments | Offset |
|---|---|---|---|---|---|
| first fragment | 1500 bytes | `0x2000` | 0 | 1 | 0 |
| second fragment | 548 bytes | `0x00b9` | 0 | 0 | 185 (byte 1480) |

Exactly as derived. The lengths add up as well: 2000 bytes of data and an 8-byte ICMP header make 2008 bytes; the first fragment carries 1500 − 20 = 1480 of them and the second 548 − 20 = 528, and 1480 + 528 = 2008.

Neither fragment has "don't fragment" set: the packet is too large to send unsplit, so `ping` does not ask for it. Both give a nonzero result with `& 0x3fff`, so pingmock ignores them. That is why `ping -s 2000` times out under pingmock, and what the README's limitation "fragmented requests are not supported" refers to.

## 7. From request to reply: only 4 bytes change

[`BuildEchoReply`](../internal/icmp/icmp.go) turns the request into a reply. Comparing the example request with the reply pingmock writes back, byte by byte, only 4 of the 84 bytes differ:

| Offset | Request | Reply | Field | Why |
|---|---|---|---|---|
| 15 | `fe` | `01` | last byte of the source address | source and destination are swapped |
| 19 | `01` | `fe` | last byte of the destination address | likewise |
| 20 | `08` | `00` | ICMP type | request 8 → reply 0 |
| 22 | `61` | `69` | high byte of the ICMP checksum | recomputed because the type changed |

Two things stand out:

- **The addresses change in only one byte each**: 10.98.0.254 and 10.98.0.1 share their first three parts, so swapping them only exchanges the last byte
- **The IP checksum does not change**: the code does recompute it, but a checksum **adds up** the fields, and swapping source and destination only swaps two of the numbers being added; the TTL is set to 64, which it already was

The identifier, sequence number and all of the data stay as they were, which matters; see section 10.

## 8. How checksums are computed

The IP header and ICMP both use the algorithm of RFC 1071: **add up the content as 16-bit numbers, fold any carry above 16 bits back into the low bits, and invert the result**. For the IP header of the example request:

```
step 1: with the checksum field taken as 0, read every 2 bytes as a number
        4500 + 0054 + 4c17 + 4000 + 4001 + 0000 + 0a62 + 00fe + 0a62 + 0001
step 2: add them                           = 0x1272f (more than 0xffff)
step 3: fold the carry into the low 16 bits  0x272f + 0x1 = 0x2730
step 4: invert every bit                   ~0x2730 = 0xd8cf
```

`0xd8cf` is exactly the value in the packet. The receiver's check is simpler still: computing the sum over the whole header **including the checksum** gives 0 when nothing is damaged.

pingmock's implementation:

```go
func Checksum(b []byte) uint16 { return ^fold(sum(0, b)) }  // step 4: invert

func sum(acc uint32, b []byte) uint32 {   // steps 1 and 2
    for len(b) >= 2 {
        acc += uint32(b[0])<<8 | uint32(b[1])  // 2 bytes make one 16-bit number
        b = b[2:]
    }
    if len(b) == 1 {
        acc += uint32(b[0]) << 8  // an odd last byte is padded with 0
    }
    return acc
}

func fold(s uint32) uint16 {   // step 3
    for s>>16 != 0 {
        s = s&0xffff + s>>16
    }
    return uint16(s)
}
```

For IPv6, the ICMP checksum also covers a **pseudo-header** of the source and destination addresses, the length and the protocol number; see `checksum6`.

## 9. What happens when a checksum is wrong

Does pingmock have to get both checksums right? Experiments say **it depends on how the other side receives the reply**, so yes.

| Wrong in the reply | The kernel | The system `ping` command (raw socket) | An unprivileged ping socket |
|---|---|---|---|
| the IP header checksum | counts `Ip InHdrErrors` and **drops it** | gets nothing | gets nothing |
| the ICMP checksum | counts `Icmp InCsumErrors` | gets it; recent iputils marks it `(BAD CHECKSUM!)`, older versions **say nothing** | gets nothing |

With a wrong ICMP checksum the receivers disagree, for two reasons:

1. **A raw socket (`SOCK_RAW`) gets its copy before the kernel checks the ICMP checksum.** On finding the checksum wrong, the kernel abandons only its own handling; the copy already given to raw sockets stays with them. An unprivileged ping socket (`SOCK_DGRAM`) is served **after** that check, so it never sees the packet.
2. **Whether iputils' `ping` reports it depends on its version.** `ping` works out in `ping4_parse_reply` whether the checksum is right (`csfailed`), but iputils had a bug: for echo replies it passed [a hard-coded `0`](https://github.com/iputils/iputils/blob/20240117/ping/ping.c#L1658) to `gather_statistics` in its place, so `(BAD CHECKSUM!)` was never printed for them. Commit [`bacf1b7`](https://github.com/iputils/iputils/commit/bacf1b7bb8555c407d065e97015319abef2a742d) ("ping: fix IPv4 checksum check always succeeding once again") fixed it, from **iputils 20240905** on:

| iputils version | For example | Echo reply with a wrong ICMP checksum |
|---|---|---|
| 20240117 and earlier | Ubuntu 24.04 | shown as usual, without a warning |
| 20240905 and later | Ubuntu 26.04 (20250605) | marked `(BAD CHECKSUM!)` at the end of the line |

With 20250605, the same reply with its checksum broken:

```
64 bytes from 10.98.0.1: icmp_seq=1 ttl=64 time=0.218 ms (BAD CHECKSUM!)
```

(Measured with iputils 20250605 built from source; 20240117 prints the same line without the warning.)

The software under test may run the system's `ping`, or send pings through a library, which often uses a ping socket. Either way a wrong checksum is noticed: the reply is dropped or flagged. So both of pingmock's checksums must be right. This is why the `icmp` tests verify the checksums of every packet they build, and why the IPv6 checksum was cross-checked against an independent implementation.

## 10. Why the data must be kept as it is

With the reply's data zeroed (and its checksum correct), `ping` prints:

```
ping: Warning: invalid tv_sec 1792292342 s
64 bytes from 10.98.0.1: icmp_seq=1 ttl=64 time=0.000 ms
wrong data byte #16 should be 0x10 but was 0x0
```

`ping` noticed two things:

- **The time of sending is gone**: `ping` computes the latency as the time the reply arrived minus the time of sending carried in the reply. With that time zeroed, there is no valid latency, so it shows 0
- **The padding is wrong**: `ping` checks that the padding is the `0x10, 0x11, 0x12...` it sent

So pingmock **leaves the data alone** and only **writes the reply later**; `ping` then measures a longer latency by itself. That is the heart of how pingmock controls latency precisely.

## 11. What differs for IPv6

IPv6 pings are built the same way; the differences show in `parse6`, `reply6` and `checksum6`:

| | IPv4 | IPv6 |
|---|---|---|
| High 4 bits of byte 0 | 4 | 6 |
| IP header length | 20 bytes, possibly with options, read from the header | **always 40 bytes** |
| IP header checksum | yes | **none** |
| Field saying what is inside | protocol = 1 | next header = 58 |
| Request / reply type | 8 / 0 | **128 / 129** |
| What the ICMP checksum covers | the ICMP part | the ICMP part + a **pseudo-header** (source, destination, length, protocol number) |
| Fragmentation | flags and offset in the IP header | a separate fragment extension header, which pingmock does not handle either |

The pseudo-header's addresses are also added up, so swapping them leaves the ICMP checksum unchanged, for the same reason the IPv4 header checksum does not change in section 7.

## 12. TCP: a connection attempt and pingmock's answers

TCP rules answer connection attempts, so the packets are TCP segments instead of ICMP. This SYN was captured from `curl http://10.96.0.1/` with the script in the appendix, changed to keep TCP (protocol 6); the answers after it are what [`BuildSynAck`](../internal/icmp/probe.go), [`BuildRST`](../internal/icmp/probe.go) and [`BuildSegment`](../internal/icmp/probe.go) build for it.

```
45 00 00 3c 06 d9 40 00 40 06 1e 25 0a 60 00 fe 0a 60 00 01   IP header, 20 bytes; protocol 6
81 50 00 50 f2 d6 87 35 00 00 00 00 a0 02 fa f0 de 12 00 00   TCP header, 20 bytes
02 04 05 b4 04 02 08 0a 85 10 d8 80 00 00 00 00 01 03 03 07   TCP options, 20 bytes
```

| Bytes (from the TCP header) | Value | Field | Meaning |
|---|---|---|---|
| 0-1 | `81 50` | source port | 33104, chosen by the client's kernel |
| 2-3 | `00 50` | destination port | 80, what the TCP rule matches on |
| 4-7 | `f2 d6 87 35` | sequence number | 4074145589, the **initial sequence number** (ISN), random per connection |
| 8-11 | `00 00 00 00` | acknowledgment number | nothing to acknowledge yet |
| 12 | `a0` | data offset | high 4 bits: 10 words of 4 bytes, so the header with its options is 40 bytes |
| 13 | `02` | flags | SYN only; the bits are FIN 0x01, SYN 0x02, RST 0x04, PSH 0x08, ACK 0x10 |
| 14-15 | `fa f0` | window | 64240 bytes the client can receive |
| 16-17 | `de 12` | checksum | over a pseudo-header and the segment, as for ICMPv6 in section 11 |
| 20-39 | | options | MSS 1460, SACK permitted, timestamps, window scale 7 |

### 12.1 How pingmock recognizes TCP: `ParseProbe`

The checks follow those of section 5, in [`ParseProbe`](../internal/icmp/probe.go), which handles every kind of packet pingmock may answer (pings, and for TCP and paths also UDP and TCP). For IPv4 (`probe4`), on the SYN above:

| Check | Where in the packet | In the example | Why |
|---|---|---|---|
| IPv4 or IPv6 | byte 0 `>> 4` | 4 | as in section 5.1 |
| Long enough, header length and total length plausible | bytes 0, 2-3 | 60 bytes, header 20 | as in section 5.2 |
| Not a fragment | bytes 6-7 `& 0x3fff` | `0x4000 & 0x3fff` = 0 | the TCP header could be cut in two |
| Carries TCP | byte 9 (IPv6: byte 6, next header, with no extension header in between) | 6 | rules out ICMP and UDP, which go other ways |
| Long enough for a TCP header | after the IP header | 40 ≥ 20 | the ports, numbers and flags are in the first 20 bytes |
| **What the flags say** | TCP byte 13 | `0x02` | see below |

The flags decide everything, with two masks:

```
flags & (SYN|ACK) == SYN    0x02 & 0x12 = 0x02   a connection attempt: Kind TCP
flags & (SYN|RST) == 0      neither SYN nor RST  a later segment of a connection: Kind Segment
anything else               SYN-ACK, or a reset  not answered
```

- **A SYN without ACK** is how every connection starts; pingmock keeps its ports and its sequence number, the ISN.
- **A later segment** (the handshake's last ACK, data, a FIN) also needs its data offset to be sane, at least 20 bytes and within the segment, because the **length of its data** is computed from it: IP total length − IP header − data offset. pingmock keeps its flags, its sequence and acknowledgment numbers and that length.
- **SYN-ACKs and resets are never answered.** pingmock sends SYN-ACKs and resets itself, and a client's kernel answers a SYN-ACK it did not expect with a reset; answering those would only start an exchange that never ends.

### 12.2 Whose segment is it

Being a TCP segment is not enough to be answered: pingmock answers only what a rule or a path made its own.

1. **A SYN to an address with a path** is a probe of traceroute or mtr, and the path answers it, as for UDP probes ([`DecideProbe`](../internal/engine/path.go)).
2. **A SYN to an address and port with a TCP rule** is a connection attempt ([`DecideTCP`](../internal/engine/tcp.go)). An attempt is identified by the addresses, the ports and the ISN: a SYN the client's kernel sends again repeats all five, and gets the answer the first one got, recorded with `"repeat": true` and not counted.
3. **Any other SYN is ignored and not recorded**, so a port without a rule looks closed by a firewall that drops, as before TCP rules existed.
4. **A later segment** is answered only if its connection was accepted under `after=close` or `after=reset` ([`DecideSegment`](../internal/engine/tcp.go)). The connection is found by its addresses and ports, and the segment must acknowledge pingmock's own sequence numbers, `70 69 6e 68` (after the SYN-ACK) or `70 69 6e 69` (after the FIN): a segment of another connection between the same ports never does. Under `after=hang`, the default, later segments are never answered.


### 12.3 The answers

The answers carry no options, so they are 40 bytes. Only the TCP header is shown below; their IP headers are the same (`45 00 00 28 00 00 00 00 40 06 65 12`, then the addresses swapped):

```
SYN-ACK  00 50 81 50  70 69 6e 67  f2 d6 87 36  50 12  ff ff  bf 95 00 00
RST      00 50 81 50  00 00 00 00  f2 d6 87 36  50 14  00 00  9e 64 00 00
FIN-ACK  00 50 81 50  70 69 6e 68  f2 d6 87 36  50 11  ff ff  bf 95 00 00
```

- **The ports are swapped**, as the addresses are: the answer comes from port 80.
- **The acknowledgment number is the ISN plus one** (`...35` becomes `...36`): a SYN counts as one byte of sequence space, and acknowledging it is what makes the client's kernel accept the answer.
- **The SYN-ACK's own sequence number is `70 69 6e 67`, "ping" in ASCII**, the same for every connection. A real server picks it at random; nothing in the client depends on it, and a fixed number is easier to recognize in a capture.
- **The reset** (`refuse`) has flags RST and ACK, sequence 0 and window 0, as a host with the port closed sends: `connect` fails with "connection refused".
- **The FIN** of `after=close` answers the client's last ACK of the handshake, which acknowledged `70 69 6e 68`; the FIN takes that number, since the SYN-ACK used up "ping" itself. Its checksum is the same as the SYN-ACK's: the sequence number went up by one and the flags down by one (0x12 to 0x11), and the one's complement sum only sees the total.

IPv6 answers are built the same way, with the 40-byte IPv6 header of section 11 and the IPv6 pseudo-header in the checksum.

## Appendix: capture packets yourself

This script creates a TUN device as pingmock does and prints the key fields of every IPv4 packet it receives, so you can check this document for yourself. It only reads and never replies, so `ping` times out, which is expected.

```python
# sudo python3 capture.py, then in another terminal: ping 10.96.0.1 (or ping -s 2000 10.96.0.1)
import fcntl, os, struct, subprocess

TUNSETIFF, IFF_TUN, IFF_NO_PI = 0x400454ca, 0x0001, 0x1000   # the same as pingmock's tun.Open
fd = os.open('/dev/net/tun', os.O_RDWR)
fcntl.ioctl(fd, TUNSETIFF, struct.pack('16sH22x', b'cap0', IFF_TUN | IFF_NO_PI))
subprocess.run('ip addr add 10.96.0.254/24 dev cap0 && ip link set cap0 up', shell=True, check=True)
print('listening on cap0 (10.96.0.0/24); press Ctrl+C to stop')

while True:
    p = os.read(fd, 65535)
    if p[0] >> 4 != 4:
        continue                                   # IPv4 only
    ihl = (p[0] & 0x0f) * 4
    flags = int.from_bytes(p[6:8], 'big')
    print(f"{len(p)} bytes  {'.'.join(map(str, p[12:16]))} -> {'.'.join(map(str, p[16:20]))}  protocol={p[9]}")
    print(f"  bytes 6-7 = 0x{flags:04x}  DF={flags >> 14 & 1}  MF={flags >> 13 & 1}  "
          f"offset={flags & 0x1fff} (byte {(flags & 0x1fff) * 8})  & 0x3fff = 0x{flags & 0x3fff:04x}")
    if p[9] == 1 and flags & 0x3fff == 0:
        icmp = p[ihl:]
        print(f"  ICMP type={icmp[0]} code={icmp[1]} id={int.from_bytes(icmp[4:6], 'big')} "
              f"seq={int.from_bytes(icmp[6:8], 'big')}")
```

`ping 10.96.0.1` shows `bytes 6-7 = 0x4000` and `ICMP type=8`; `ping -s 2000 10.96.0.1` shows the two fragments of section 6.
