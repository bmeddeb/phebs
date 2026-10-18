namespace go packet
struct Envelope {
  1: required string key
  7: optional i64 count
  9: optional bool ready
  13: optional binary payload
}
struct Ambiguous {
  1: optional string left
  1: optional string right
}
service Probe {
  string check()
}
