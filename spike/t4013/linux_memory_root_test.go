package t4013

import "testing"

func TestLinuxMemoryHierarchyRoot(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*LinuxMemoryObservation)
		accept bool
	}{
		{"absent root counters", func(*LinuxMemoryObservation) {}, true},
		{"root only", func(o *LinuxMemoryObservation) { o.CgroupAncestors = o.CgroupAncestors[:1] }, true},
		{"root is not literal max", func(o *LinuxMemoryObservation) { o.CgroupAncestors[0].Unlimited = true }, false},
		{"root has limit", func(o *LinuxMemoryObservation) { o.CgroupAncestors[0].LimitBytes = 1 }, false},
		{"root has current", func(o *LinuxMemoryObservation) { o.CgroupAncestors[0].CurrentBytes = 1 }, false},
		{"root has parent", func(o *LinuxMemoryObservation) { o.CgroupAncestors[0].ParentIdentity = digest([]byte("parent")) }, false},
		{"later root", func(o *LinuxMemoryObservation) {
			o.CgroupAncestors[1].HierarchyRoot = true
			o.CgroupAncestors[1].LimitBytes = 0
			o.CgroupAncestors[1].CurrentBytes = 0
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation, contract := linuxMemoryTestInputs()
			observation.CgroupAncestors[0].HierarchyRoot = true
			observation.CgroupAncestors[0].LimitBytes = 0
			observation.CgroupAncestors[0].CurrentBytes = 0
			test.change(&observation)
			got, err := EvaluateLinuxMemoryAdmission(observation, contract)
			if (err == nil) != test.accept || !test.accept && got != (LinuxMemoryCalculation{}) {
				t.Fatalf("root calculation = %+v, %v; accept=%v", got, err, test.accept)
			}
		})
	}
}
