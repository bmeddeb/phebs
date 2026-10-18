namespace go neutral

struct Parcel {
  1: required string code
  7: optional i64 count
  9: optional list<string> tags
  13: optional map<string, string> attrs
}

service Messenger {
  string send(1: Parcel parcel)
}
