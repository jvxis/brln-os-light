"""Price-response dataset (phase 0 of docs/autofee-adaptive-pricing-plan.md).

For every channel: our fee changes (channel detail fee_logs, side "local")
over 30 days define segments of constant fee; forwards give the sales inside
each segment. Output: per channel, per fee level -> days observed, volume per
day, fee per day, forwards per day. Only channels with at least two levels
observed for one day or more are printed, and only when they earned 100 sats.

usage:
  python autofee_price_response.py <label> <cookie file> <base url> <forwards.json|detail>

<forwards.json>: a JSON list of LND forwarding events with chan_id_out,
timestamp, amt_out and fee_msat, e.g.
  lncli fwdinghistory --start_time $(date -d "30 days ago" +%s) --max_events 50000
"detail": no LND access; uses the detail's "routed" list instead (capped).

The cookie file comes from a login to /api/auth/login (curl -c). Read-only.
Known limits: no balance history, so zero sales at a level can be zero demand
or zero stock; short levels and third-party bursts are single points.
"""
import json, subprocess, sys, datetime, collections

label, cookie, base, fwdpath = sys.argv[1:5]


def get(path):
    out = subprocess.run(["curl", "-sk", "-b", cookie, "-H", "Origin: " + base, base + path], capture_output=True).stdout
    return json.loads(out.decode("utf-8"))


now = datetime.datetime.now(datetime.timezone.utc)
chans = get("/api/lnops/channels")["channels"]
fwd = collections.defaultdict(list)
if fwdpath != "detail":
    for e in json.load(open(fwdpath, encoding="utf-8")):
        fwd[str(e["chan_id_out"])].append((int(e["timestamp"]), int(e["amt_out"]), int(e["fee_msat"]) / 1000))
rows = []
for c in chans:
    d = get("/api/lnops/channel/detail?channel_point=%s&limit=500" % c["channel_point"])
    logs = [l for l in d.get("fee_logs") or [] if str(l.get("side", "")).lower() in ("ours", "local", "our", "self")]
    scid = str(c.get("channel_id_str") or c.get("channel_id"))
    if fwdpath == "detail":
        # no LND access: use the detail's routed list (newest first, capped)
        for r in d.get("routed") or []:
            if str(r.get("chan_id_out")) == scid and str(r.get("status", "")).lower() in ("", "settled", "succeeded", "success"):
                fwd[scid].append((datetime.datetime.fromisoformat(r["occurred_at"].replace("Z", "+00:00")).timestamp(), int(r.get("amount_out_sat") or 0), float(r.get("fee_sat") or 0)))
    events = sorted(fwd.get(scid, []))
    # segments: from each change to the next
    changes = sorted((datetime.datetime.fromisoformat(l["captured_at"].replace("Z", "+00:00")).timestamp(), int(l["new_fee_rate_ppm"])) for l in logs)
    if not changes:
        continue
    start30 = (now - datetime.timedelta(days=30)).timestamp()
    # a channel younger than 30 days only has history since it opened
    cur, opened = d.get("current_block_height") or 0, d.get("open_block_height") or 0
    if cur and opened and cur >= opened:
        start30 = max(start30, now.timestamp() - (cur - opened) * 600)
    # initial level before the first change inside the window
    first = changes[0]
    segs = []
    if first[0] > start30 and logs:
        old = [l for l in logs if l.get("old_fee_rate_ppm") is not None]
        if old:
            segs.append((start30, first[0], int(sorted(old, key=lambda l: l["captured_at"])[0]["old_fee_rate_ppm"])))
    for i, (t, fee) in enumerate(changes):
        end = changes[i + 1][0] if i + 1 < len(changes) else now.timestamp()
        if end <= start30:
            continue
        segs.append((max(t, start30), end, fee))
    level = collections.defaultdict(lambda: [0.0, 0, 0.0, 0])  # fee -> days, vol, fee, n
    for lo, hi, fee in segs:
        days = (hi - lo) / 86400
        if days <= 0:
            continue
        x = level[fee]
        x[0] += days
        for (t, amt, f) in events:
            if lo <= t < hi:
                x[1] += amt; x[2] += f; x[3] += 1
    usable = {f: v for f, v in level.items() if v[0] >= 1}
    if len(usable) < 2:
        continue
    rows.append((c["peer_alias"], c["capacity_sat"], usable))

print("=" * 10, label, "canais com >=2 niveis de fee observados >=1 dia:", len(rows))
for alias, cap, usable in sorted(rows, key=lambda r: -sum(v[2] for v in r[2].values())):
    tot_fee = sum(v[2] for v in usable.values())
    if tot_fee < 100:
        continue
    print("\n%-22s cap %.1fM" % (str(alias)[:22], cap / 1e6))
    print("   %6s %6s %9s %8s %6s" % ("fee", "dias", "vende/dia", "sats/dia", "n/dia"))
    for fee, v in sorted(usable.items()):
        print("   %6d %6.1f %8.2fM %8.0f %6.1f" % (fee, v[0], v[1] / v[0] / 1e6, v[2] / v[0], v[3] / v[0]))
