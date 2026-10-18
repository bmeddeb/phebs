#!/usr/bin/env python3
"""Independent source-only complete identifier oracle, before any indexing.

No SCIP/production imports, emitted facts or candidate output are read. Tables
below are manual source/IDL/generated-module identities; regex only locates
their exact source spans. Existing changed label/freeze bytes are refused.
"""
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent
KEYWORDS = set("break default func interface select case defer go map struct chan else goto package switch const fallthrough if range type continue for import return var".split())
FIELDS = {"Key": ("packet.Envelope", 1), "Count": ("packet.Envelope", 7), "Ready": ("packet.Envelope", 9), "Payload": ("packet.Envelope", 13)}


def digest(raw):
    return "sha256:" + hashlib.sha256(raw).hexdigest()


def tokens(raw):
    pattern = rb'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'|[A-Za-z_][A-Za-z_0-9]*'
    for found in re.finditer(pattern, raw):
        text = found.group().decode("utf-8")
        if re.fullmatch("[A-Za-z_][A-Za-z_0-9]*", text) and text not in KEYWORDS:
            yield found.start(), found.end(), text


def retain(path, raw):
    target = ROOT / path
    if target.exists():
        assert target.read_bytes() == raw, "frozen oracle changed; independent review required"
    else:
        with target.open("xb") as stream:
            stream.write(raw)


def main():
    rows = []
    for population, path in (("public", "common/types/mapper/thrift/health.go"), ("neutral", "use/use.go")):
        raw = (ROOT / "testdata" / population / path).read_bytes()
        for start, end, text in tokens(raw):
            prefix = raw[max(0, start-20):start]
            identity = None
            expected = "negative"
            reason = "Ordinary Go type/package/local/method identifier or same-named internal/lookalike field; not a supported generated field use"
            if population == "public":
                # Only the generated composite is a write target; the second
                # composite is ordinary internal types.HealthStatus.
                generated_start = raw.index(b"return &health.HealthStatus{")
                generated_end = raw.index(b"\n\t}", generated_start)
                if generated_start < start < generated_end and text in ("Ok", "Msg") and re.match(rb"\s*:", raw[end:]):
                    identity = ("health.HealthStatus", 1 if text == "Ok" else 2)
                # This function's receiver is the generated health type.
                reverse_start = raw.index(b"func ToHealthStatus(")
                if start > reverse_start and prefix.endswith(b"t.") and text in ("Ok", "GetMsg"):
                    identity = ("health.HealthStatus", 1 if text == "Ok" else 2)
            else:
                if text in FIELDS and (prefix.endswith(b"v.") or re.match(rb"\s*:", raw[end:])):
                    identity = FIELDS[text]
                if text in ("GetKey", "IsSetKey") and prefix.endswith(b"v."):
                    identity = FIELDS["Key"]
                if text in ("Success", "GetSuccess") and prefix.endswith(b"r."):
                    identity = ("packet.Probe_Check_Result", 0)
                if text in ("Identifier", "GetIdentifier") and prefix.endswith(b"n."):
                    identity = ("packet.Envelope", 1)
                if text in ("ComputeKey", "Left", "Right") and (prefix.endswith(b"v.") or prefix.endswith(b"bad.")):
                    expected = "unsupported"
                    reason = "Non-accessor helper or ambiguous duplicate embedded-IDL field identity; no unique supported field binding"
            if identity is not None:
                expected = "positive"
                reason = "Manual source field/accessor identity bound to exact embedded IDL and independent generated field/wire-ID witness"
            row = dict(population=population, path=path, source_digest=digest(raw), start_byte=start, end_byte=end, line=raw.count(b"\n", 0, start)+1, text=text, expected=expected, reason=reason)
            if identity is not None:
                row.update(message=identity[0], field_number=identity[1], object=identity[0]+"#"+str(identity[1]), tier="exact", generator="thriftrw", source_binding="module_digest")
            rows.append(row)
        if population == "neutral":
            marker = b'FieldByName("Key")'
            assert raw.count(marker) == 1
            start = raw.index(marker) + len(b'FieldByName("')
            rows.append(dict(population=population, path=path, source_digest=digest(raw), start_byte=start, end_byte=start+3, line=raw.count(b"\n", 0, start)+1, text="Key", expected="unsupported", reason="Reflection string is not an exact indexed field-use identifier"))
    rows.sort(key=lambda row:(row["population"], row["path"], row["start_byte"]))
    counts = {}
    for row in rows:
        key = row["population"]+"/"+row["expected"]
        counts[key] = counts.get(key, 0)+1
    assert counts["public/positive"] == 4, counts
    assert counts["neutral/positive"] == 15, counts
    assert counts["neutral/unsupported"] == 4, counts
    data = "".join(json.dumps(row, sort_keys=True, ensure_ascii=False)+"\n" for row in rows).encode()
    retain("source-labels.jsonl", data)
    files = []
    for path in sorted((ROOT / "testdata").rglob("*")):
        if path.is_file():
            raw = path.read_bytes()
            files.append(dict(path=str(path.relative_to(ROOT)), bytes=len(raw), digest=digest(raw)))
    freeze = dict(schema="t522-source-label-freeze-v1", record_date="2026-10-18", candidate_executed=False, indexer_executed=False, generator_executed=False, counts=counts, labels_digest=digest(data), labels_bytes=len(data), files=files)
    retain("source-freeze.json", (json.dumps(freeze, indent=2)+"\n").encode())
    print(json.dumps(dict(counts=counts, source_files=len(files), source_bytes=sum(f["bytes"] for f in files), labels_digest=digest(data))))


if __name__ == "__main__":
    main()
