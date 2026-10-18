# The round trip of one ping

English | [中文](packet-path.zh-CN.md)

This document follows one ping request from the `ping` program through the kernel to pingmock, and the reply back to `ping`. Once you understand this, you understand why pingmock controls the latency software sees **without any change to that software**.

The example uses the defaults: pingmock's virtual device is `pingmock0` with the address `10.99.0.254/24`, there is a rule `10.99.0.1=50ms`, and a program runs `ping 10.99.0.1`.

## The whole picture

```
Outbound (the request)
  ping ──sendto("10.99.0.1")──► kernel
                                 │ ① route lookup
                                 │    the local table first: 10.99.0.1 is not an address of this machine
                                 │    then the main table: longest prefix match → 10.99.0.0/24 dev pingmock0 src 10.99.0.254
                                 │ ② IP header: destination 10.99.0.1, source taken from the route, 10.99.0.254
                                 ▼
                           pingmock0 (TUN driver) ──► queue ──► pingmock's Read() gets it

Inside pingmock
  decide from the rules → build the reply → wait 50ms

Return (the reply)
  pingmock's Write() ──► pingmock0 ──► the kernel treats it as "a packet received on pingmock0"
                                        │ ③ reverse path check: a reply to 10.99.0.1 would also leave through pingmock0, so it passes
                                        │ ④ destination 10.99.0.254 is in the local table: for this machine, accept it
                                        │ ⑤ an ICMP echo reply: deliver it to ping's socket
                                        ▼
                                      ping's recvmsg() gets the reply and computes the latency: 50ms
```

`ping` only ever talks to the kernel and never knows pingmock exists. The kernel hands the request to pingmock because of the routing table, and hands pingmock's reply to `ping` as if it came from the network.

## Setting up: what pingmock does at startup

What pingmock does to the system at startup comes down to **creating a TUN device in code and running a few `ip` commands for you**.

### The commands it runs

Recording the processes pingmock starts with `strace` shows every command it runs. For `pingmock -rule 10.99.0.1=10ms -rule 8.8.8.8=20ms`:

```bash
ip addr add 10.99.0.254/24 dev pingmock0          # ① give the device its address and network
ip link set pingmock0 up                          # ② bring the device up
ip -6 addr add fd99::fe/64 dev pingmock0 nodad    # ③ the IPv6 address
ip route replace 8.8.8.8/32 dev pingmock0         # ④ a route for a real address outside the network
```

| Command | Code | Notes |
|---|---|---|
| ① ② | [`configureDevice`](../internal/app/instance.go) | The address comes from `-addr` (or `addr` in a configuration file), `10.99.0.254/24` by default |
| ③ | [`configureDevice`](../internal/app/instance.go) | The address comes from `-addr6`, `fd99::fe/64` by default. If the kernel has no IPv6, this command fails and pingmock says so and continues with IPv4 only |
| ④ | [`Router.Ensure`](../internal/tun/tun_linux.go) | Only for targets **outside the network**. 10.99.0.1 is in 10.99.0.0/24, for which the kernel already added a route after command ①, so nothing is run for it |

Every command goes through [`tun.IP`](../internal/tun/tun_linux.go), which runs the system's `ip` program (from iproute2).

Under `pingmock exec`, one more command comes first:

```bash
ip link set lo up                                 # bring up the loopback device
```

exec creates a brand new network namespace, in which even the loopback device `lo` starts down, and the control API listens on `127.0.0.1:9100`, which needs it. In the normal mode the system's `lo` is up already. The code is in [`runExecChild`](../internal/app/exec_linux.go).

### Creating the device: in code, not with `ip`

The commands above do not create the device. pingmock does that in code, in [`tun.Open`](../internal/tun/tun_linux.go):

1. it opens `/dev/net/tun`, the kernel's entry point for TUN devices;
2. it calls `ioctl(TUNSETIFF)` to tell the kernel "create a TUN device named pingmock0 and bind it to the file I just opened".

From then on, reading and writing the file means receiving and sending packets on the device. `ip tuntap add dev pingmock0 mode tun` can create a TUN device too; the difference is how long the device lives:

| | `ip tuntap add` | pingmock's `ioctl` |
|---|---|---|
| When the device goes away | Never on its own; it must be deleted | **When the file that opened it is closed**, that is, when pingmock exits |
| `ip -d link` shows | `persist on` | `persist off` |
| Who reads and writes the other end | Nobody; without a program the device shows `NO-CARRIER` | The pingmock process |

pingmock uses the second, so **however it exits, the kernel removes the device with its addresses and routes**, and nothing is left on the system.

### The routes the kernel adds

After command ①, **the kernel itself** adds an entry to two tables:

| Table | Entry | Meaning |
|---|---|---|
| local | `local 10.99.0.254 dev pingmock0` | 10.99.0.254 is an address of this machine |
| main | `10.99.0.0/24 dev pingmock0 src 10.99.0.254` | 10.99.0.0 to 10.99.0.255 are reached through pingmock0 |

pingmock does not edit the routing table. `10.99.0.254/24` says two things at once: the device's own address is 10.99.0.254, and its network is the addresses that share the first 24 bits (`10.99.0`). The kernel concludes that this network is on pingmock0's side and adds the route in the main table.

### Try it: build the same setup by hand

Without pingmock, a few `ip` commands build almost the same setup and show at which step the route appears (with `10.97.0.x`, to keep clear of pingmock):

```bash
sudo ip tuntap add dev demo0 mode tun          # 1. create a TUN device
ip route get 10.97.0.1                         #    → still the default route via eth0: a device alone tells the kernel nothing

sudo ip link set demo0 up                      # 2. bring it up
ip route get 10.97.0.1                         #    → still eth0

sudo ip addr add 10.97.0.254/24 dev demo0      # 3. give it an address and network
ip route show | grep 10.97                     #    → 10.97.0.0/24 dev demo0 proto kernel appears, added by the kernel
ip route get 10.97.0.1                         #    → now demo0

sudo ip addr del 10.97.0.254/24 dev demo0      # 4. remove the address
ip route get 10.97.0.1                         #    → the route is gone, back to eth0

sudo ip tuntap del dev demo0 mode tun          # clean up
```

**The route appears in step 3, when the address is set**, not when the device is created or brought up. The only difference from pingmock's setup is that no program reads and writes the other end of the device, so a ping to 10.97.0.1 gets no reply. A program that opens the device, reads the request, swaps its addresses and writes it back is enough for ping to get a reply, and that is what pingmock does.

## Outbound: how the request reaches pingmock

### Step 0: ping hands the request to the kernel

`ping` opens an ICMP socket and calls `sendto` with just two things: **64 bytes of ICMP** (an 8-byte ICMP header and 56 bytes of data, starting with the time of sending) and **the destination 10.99.0.1**. It says nothing about which device to use and does not write the IP header; the kernel decides those.

### Step ①: route lookup

The kernel decides where the packet goes. The routing rules are consulted in order, as `ip rule` shows:

```
0:      from all lookup local     ← first
32766:  from all lookup main      ← then
```

- **The local table**: is the destination an address of this machine? 10.99.0.1 is not, so on to the next.
- **The main table**: among the routes that contain 10.99.0.1, take the one with the **most specific prefix**, which is called longest prefix match:

| Route | Contains 10.99.0.1? | Specificity |
|---|---|---|
| `default` (0.0.0.0/0) | yes | /0, every address |
| `10.99.0.0/24 dev pingmock0` | yes | **/24, more specific: chosen** |

The result is `10.99.0.1 dev pingmock0 src 10.99.0.254`: leave through pingmock0, with 10.99.0.254 as the source address.

### Step ②: the IP header

The kernel puts a 20-byte IP header in front of the 64 bytes of ICMP: destination 10.99.0.1 and **the source address from the route lookup, 10.99.0.254**. The route has to be looked up before the source can be filled in, because the source depends on the device the packet leaves through. The result is an 84-byte IP packet.

### Step ③: to the TUN driver, and to pingmock

The kernel asks pingmock0's driver to send the packet. A real network card's driver would turn it into signals on a wire; **the TUN driver puts it in a queue** attached to the `/dev/net/tun` file pingmock opened.

pingmock's main loop, [`Responder.Serve`](../internal/responder/responder.go), keeps reading that file, so it reads these 84 bytes. To the kernel, it "sent" a packet, and the device's transmit counter (`tx_packets`) goes up by one; to pingmock, it "read" a packet.

## Inside pingmock

Having read the packet, [`Responder.handle`](../internal/responder/responder.go):

1. checks with [`ParseEchoRequest`](../internal/icmp/icmp.go) that it is a ping request and takes its destination, 10.99.0.1;
2. asks [`RuleSet.Decide`](../internal/engine/ruleset.go) what to do according to the rules: reply, drop or answer unreachable, and after how long. Here: reply after 50ms;
3. builds the reply with [`BuildEchoReply`](../internal/icmp/icmp.go): **it swaps the source and destination addresses**, changes the ICMP type from 8 (request) to 0 (reply), recomputes the checksums, and **keeps everything else as it was**, including the time of sending that ping put in the data.

`Serve` then writes the reply to the device after 50ms with `time.AfterFunc`. If the rule drops the request, nothing is written. See the [code tour](architecture.md) for more, and [Inside the packets](packets.md) for what every byte of the request and reply means.

## Return: how the reply gets back to ping

### Step ④: the kernel receives a packet

When pingmock `Write`s the reply to the file, the TUN driver hands it to the kernel as "a packet pingmock0 received from the network", and the device's receive counter (`rx_packets`) goes up by one. For the kernel this is no different from a real network card receiving a packet.

### Step ⑤: the reverse path check

Many Linux systems perform a **reverse path check** (`rp_filter`): on receiving a packet from 10.99.0.1, the kernel asks "if I replied to 10.99.0.1, which device would that leave through?" If it is not the device the packet came in on, it may drop the packet as spoofed.

A reply to 10.99.0.1 leaves through pingmock0, the very device the reply came in on, so the check passes, even in its strictest mode. The same holds for hijacked real addresses (below), because the route pingmock adds for them also points to pingmock0.

### Step ⑥: is it for this machine?

The reply's destination is 10.99.0.254. The kernel finds it in the local table, an address of this machine, so it accepts the packet instead of forwarding it.

### Step ⑦: delivered to ping

The kernel sees an ICMP echo reply and delivers it to ping's socket. Several pings may run at once; ping recognizes its own replies by the **identifier** in the ICMP header, which differs between ping processes, and the **sequence number** tells it which request a reply answers.

Finally, ping subtracts the **time of sending**, carried back in the reply's data, from the current time. pingmock waited 50ms before writing the reply, so ping finds 50ms. pingmock never touches the timestamp; it only **writes the reply later**.

## Two follow-up questions

### Why is the device's own address, 10.99.0.254, not mocked?

Because the local table stops it in step ①: 10.99.0.254 is an address of this machine, so the kernel answers it itself over the loopback device `lo` and the packet never reaches pingmock0. That is why every address in the subnet **except the device's own** is a mocked host.

### How does pingmock mock a real address such as 8.8.8.8?

8.8.8.8 is not in 10.99.0.0/24, so it normally follows the default route to the real network. When a rule targets it, [`Router.Ensure`](../internal/tun/tun_linux.go) runs:

```bash
ip route replace 8.8.8.8/32 dev pingmock0
```

`/32` contains only 8.8.8.8, the most specific route there can be, so it wins over `default` in the longest prefix match of step ①, and packets to 8.8.8.8 go to pingmock0 too. Its neighbor 8.8.4.4 is not affected. When pingmock exits, the device is removed and the route with it.

## IPv6

IPv6 follows the same path, except that the device's address is `fd99::fe/64`, for which the kernel likewise adds the route `fd99::/64 dev pingmock0`, and ping requests and replies are ICMPv6 types 128 and 129. pingmock adds the IPv6 address with `nodad`, which skips duplicate address detection (DAD); otherwise the address would only be usable once detection finishes, after about a second by default on Linux.

## See it for yourself

While pingmock runs, these commands show each step above:

| To see | Command | You should see |
|---|---|---|
| The device and its address | `ip addr show pingmock0` | `inet 10.99.0.254/24` |
| The route the kernel added | `ip route show` | `10.99.0.0/24 dev pingmock0 proto kernel` |
| The order of the routing rules | `ip rule` | `0: lookup local` before `32766: lookup main` |
| The route lookup of step ① | `ip route get 10.99.0.1` | `dev pingmock0 src 10.99.0.254` |
| The device's own address stays local | `ip route get 10.99.0.254` | `local 10.99.0.254 dev lo` |
| What ping hands the kernel | `strace -e sendto ping -c 1 10.99.0.1` | 64 bytes starting with `\10\0` (type 8) |
| Packets each way | `cat /sys/class/net/pingmock0/statistics/{tx,rx}_packets` | each answered request adds one to both; a dropped one only to tx |
| The route for a hijacked address | `ip route show` with `-rule 8.8.8.8=30ms` | `8.8.8.8 dev pingmock0` |
| The commands pingmock runs | `sudo strace -f -e trace=execve pingmock -rule 8.8.8.8=30ms` | `ip addr add`, `ip link set ... up`, `ip -6 addr add`, `ip route replace` |
