# Test fixtures

`cellwan/` holds `cellwan_status` responses; `stats/` holds the expected output for each
one: `<name>.golden.json` (the log line) and `<name>.golden.txt` (the terminal report).
Do not edit the goldens by hand. After adding a fixture or changing the output on purpose,
run `just golden` (`go test ./internal/stats -update`) and review the diff.

Fixtures marked `"_synthetic": true` at the top level are hand-written; the decoder ignores
the key.

No fixture may contain real identifiers. IMEI, IMSI, ICCID, cell ID, eNB ID, TAC and PCI
values are placeholders.

## `cellwan/`

| File                  | Provenance                                                                                                                                                                                                                                                                                                                                               |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `fwa505-V1.70.json`   | Captured on 2026-09-17 from a Zyxel Nebula FWA505, firmware `V1.70(ACKO.0)C0`, over HTTPS in plaintext mode. Anonymised: `INTF_IMEI`, `USIM_IMSI`, `USIM_ICCID`, `INTF_Cell_ID`, `INTF_TAC`, `INTF_PhyCell_ID` and `NSA_PhyCellID` are placeholders; everything else, including the two idle `SCC_Info` slots (`Enable: true`, empty band), is verbatim. |
| `lte-plain.json`      | Synthetic. LTE-only response in the older firmware style: numeric 3GPP bandwidth index, numeric `INTF_RFCN`, `NSA_PCI` alias, empty `SCC_Info`. Values follow the shape of the 2025 log lines. Kept to exercise the `Bandwidth` index branch even after B5.                                                                                              |
| `envelope.json`       | Synthetic. A `{content, iv}` encrypted envelope used only to test `IsEncryptedEnvelope` and the `ErrEncryptedEnvelope` path. The strings are base64 text, not real ciphertext.                                                                                                                                                                           |
| `missing-fields.json` | Synthetic. `Object[0]` with only `INTF_Status`: every derived value falls back to `N/A`, `None`, `0` or absent. Exercises every missing-value path.                                                                                                                                                                                                      |
| `lte-ca-remap.json`   | Synthetic. LTE with PLMN `22201` (remapped to `2221`), `LTE_BC7` band naming, numeric bandwidth indices, a decimal RSRP, and four `SCC_Info` entries (two shown, one with an empty band and one disabled are filtered).                                                                                                                                  |
| `plmn-22250.json`     | Synthetic. NSA with PLMN `22250` and a six-digit eNB id (remapped to `22288`), lowercase band names, empty bandwidths on one side, string `INTF_RFCN`.                                                                                                                                                                                                   |
