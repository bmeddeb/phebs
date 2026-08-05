package sourceobservation

import (
	"errors"
	"sort"
)

type MethodDescriptor struct {
	Protocol     string
	ImportPath   string
	ClientType   string
	Method       string
	Operation    string
	Constructors []string
}

type CallerEvidence struct {
	State     string // resolved | ambiguous | dynamic | unsupported
	Operation string
	Reason    string
	Span      Span
}

type CallerResult struct {
	Protocol string
	Evidence []CallerEvidence
}

// AdaptCallers projects one protocol independently from the shared source
// observation. A failure or ambiguity in one invocation cannot affect another
// protocol's result.
func AdaptCallers(value Observation, protocol string, descriptors []MethodDescriptor) (CallerResult, error) {
	if err := Validate(value); err != nil {
		return CallerResult{}, err
	}
	if protocol != "grpc" && protocol != "thrift" {
		return CallerResult{}, errors.New("unsupported caller protocol")
	}
	for _, descriptor := range descriptors {
		if descriptor.Protocol != protocol || descriptor.ImportPath == "" || descriptor.ClientType == "" ||
			descriptor.Method == "" || descriptor.Operation == "" {
			return CallerResult{}, errors.New("invalid caller descriptor")
		}
	}
	result := CallerResult{Protocol: protocol, Evidence: []CallerEvidence{}}
	for _, fn := range value.Functions {
		for _, call := range fn.Calls {
			var matches []MethodDescriptor
			for _, descriptor := range descriptors {
				if descriptor.Method != call.Selector {
					continue
				}
				for _, binding := range call.Bindings {
					if binding.ImportPath != descriptor.ImportPath {
						continue
					}
					if binding.TypeName == descriptor.ClientType || contains(descriptor.Constructors, binding.Constructor) {
						matches = append(matches, descriptor)
						break
					}
				}
			}
			matches = uniqueMethods(matches)
			switch len(matches) {
			case 1:
				result.Evidence = append(result.Evidence, CallerEvidence{State: "resolved", Operation: matches[0].Operation, Span: call.Span})
			case 0:
				if call.Receiver != "resolved" && hasCandidateMethod(value, descriptors, call.Selector) {
					result.Evidence = append(result.Evidence, CallerEvidence{State: call.Receiver, Reason: call.Reason, Span: call.Span})
				}
			default:
				result.Evidence = append(result.Evidence, CallerEvidence{State: "ambiguous", Reason: "ambiguous_receiver_provenance", Span: call.Span})
			}
		}
	}
	return result, nil
}

type KafkaEvidence struct {
	State      string
	Topic      string
	Reason     string
	Library    string
	ImportPath string
	Shape      string
	Binding    string
	GroupID    string
	Span       Span
}

type KafkaResult struct {
	Plane    string
	Evidence []KafkaEvidence
}

func AdaptKafka(value Observation, plane string) (KafkaResult, error) {
	if err := Validate(value); err != nil {
		return KafkaResult{}, err
	}
	if plane != "producer" && plane != "consumer" {
		return KafkaResult{}, errors.New("unsupported Kafka plane")
	}
	result := KafkaResult{Plane: plane, Evidence: []KafkaEvidence{}}
	for _, site := range value.TopicSites {
		if site.Plane != plane {
			continue
		}
		result.Evidence = append(result.Evidence, KafkaEvidence{
			State: site.State, Topic: site.Topic, Reason: site.Reason,
			Library: site.Library, ImportPath: site.ImportPath, Shape: site.Shape,
			Binding: site.Binding, GroupID: site.GroupID, Span: site.Span,
		})
	}
	return result, nil
}

func contains(values []string, wanted string) bool {
	if wanted == "" {
		return false
	}
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func hasCandidateMethod(observation Observation, values []MethodDescriptor, method string) bool {
	for _, value := range values {
		if value.Method == method && importsPath(observation.Imports, value.ImportPath) {
			return true
		}
	}
	return false
}

func importsPath(values []Import, path string) bool {
	for _, value := range values {
		if value.Path == path && value.Kind != "blank" {
			return true
		}
	}
	return false
}

func uniqueMethods(values []MethodDescriptor) []MethodDescriptor {
	sort.Slice(values, func(i, j int) bool {
		if values[i].Operation != values[j].Operation {
			return values[i].Operation < values[j].Operation
		}
		if values[i].ImportPath != values[j].ImportPath {
			return values[i].ImportPath < values[j].ImportPath
		}
		return values[i].ClientType < values[j].ClientType
	})
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1].Operation != value.Operation {
			out = append(out, value)
		}
	}
	return out
}
