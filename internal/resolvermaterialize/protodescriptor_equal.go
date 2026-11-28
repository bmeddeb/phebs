package resolvermaterialize

import (
	"slices"
	"strings"

	"google.golang.org/protobuf/types/descriptorpb"
)

// descriptorsEquivalent reports whether a protoc-gen-go descriptor (linked:
// fully qualified type names, interpreted options) describes the same file as
// an unlinked parse of a declared .proto (type names as written, options
// uninterpreted). It compares package, syntax or edition, imports,
// go_package, messages, enums, extensions counts and services. Options other
// than go_package, json names, defaults and reserved ranges are not compared.
func descriptorsEquivalent(generated, declared *descriptorpb.FileDescriptorProto) bool {
	if generated == nil || declared == nil ||
		generated.GetPackage() != declared.GetPackage() ||
		descriptorSyntax(generated) != descriptorSyntax(declared) ||
		descriptorSyntax(generated) == "editions" && generated.GetEdition() != declared.GetEdition() ||
		!slices.Equal(generated.GetDependency(), declared.GetDependency()) ||
		!slices.Equal(generated.GetPublicDependency(), declared.GetPublicDependency()) ||
		!slices.Equal(generated.GetWeakDependency(), declared.GetWeakDependency()) ||
		generatedGoPackage(generated) != declaredGoPackage(declared) ||
		len(generated.GetExtension()) != len(declared.GetExtension()) {
		return false
	}
	scope := ""
	if declared.GetPackage() != "" {
		scope = "." + declared.GetPackage()
	}
	local := newLocalProtoNames(declared)
	if !messagesEquivalent(generated.GetMessageType(), declared.GetMessageType(), scope, local) ||
		!enumsEquivalent(generated.GetEnumType(), declared.GetEnumType()) ||
		len(generated.GetService()) != len(declared.GetService()) {
		return false
	}
	for index, service := range generated.GetService() {
		other := declared.GetService()[index]
		if service.GetName() != other.GetName() || len(service.GetMethod()) != len(other.GetMethod()) {
			return false
		}
		for methodIndex, method := range service.GetMethod() {
			otherMethod := other.GetMethod()[methodIndex]
			if method.GetName() != otherMethod.GetName() ||
				method.GetClientStreaming() != otherMethod.GetClientStreaming() ||
				method.GetServerStreaming() != otherMethod.GetServerStreaming() ||
				!local.resolves(method.GetInputType(), otherMethod.GetInputType(), scope) ||
				!local.resolves(method.GetOutputType(), otherMethod.GetOutputType(), scope) {
				return false
			}
		}
	}
	return true
}

func descriptorSyntax(file *descriptorpb.FileDescriptorProto) string {
	if file.GetSyntax() == "" {
		return "proto2"
	}
	return file.GetSyntax()
}

func generatedGoPackage(file *descriptorpb.FileDescriptorProto) string {
	return file.GetOptions().GetGoPackage()
}

// declaredGoPackage reads go_package from an unlinked parse, where the option
// is either interpreted or still an uninterpreted simple string option.
func declaredGoPackage(file *descriptorpb.FileDescriptorProto) string {
	if value := file.GetOptions().GetGoPackage(); value != "" {
		return value
	}
	for _, option := range file.GetOptions().GetUninterpretedOption() {
		if len(option.GetName()) == 1 && !option.GetName()[0].GetIsExtension() &&
			option.GetName()[0].GetNamePart() == "go_package" {
			return string(option.GetStringValue())
		}
	}
	return ""
}

func messagesEquivalent(
	generated, declared []*descriptorpb.DescriptorProto,
	scope string,
	local localProtoNames,
) bool {
	if len(generated) != len(declared) {
		return false
	}
	for index, message := range generated {
		other := declared[index]
		if message.GetName() != other.GetName() ||
			len(message.GetField()) != len(other.GetField()) ||
			len(message.GetExtension()) != len(other.GetExtension()) ||
			len(message.GetOneofDecl()) != len(other.GetOneofDecl()) ||
			!enumsEquivalent(message.GetEnumType(), other.GetEnumType()) {
			return false
		}
		messageScope := scope + "." + message.GetName()
		for oneofIndex, oneof := range message.GetOneofDecl() {
			if oneof.GetName() != other.GetOneofDecl()[oneofIndex].GetName() {
				return false
			}
		}
		for fieldIndex, field := range message.GetField() {
			if !fieldEquivalent(field, other.GetField()[fieldIndex], messageScope, local) {
				return false
			}
		}
		if !messagesEquivalent(message.GetNestedType(), other.GetNestedType(), messageScope, local) {
			return false
		}
	}
	return true
}

func fieldEquivalent(
	generated, declared *descriptorpb.FieldDescriptorProto,
	scope string,
	local localProtoNames,
) bool {
	if generated.GetName() != declared.GetName() ||
		generated.GetNumber() != declared.GetNumber() ||
		generated.GetLabel() != declared.GetLabel() ||
		generated.GetProto3Optional() != declared.GetProto3Optional() ||
		(generated.OneofIndex == nil) != (declared.OneofIndex == nil) ||
		generated.GetOneofIndex() != declared.GetOneofIndex() {
		return false
	}
	if declared.Type != nil {
		// A scalar, or a group/map entry the parser already typed: the
		// generated descriptor must agree exactly.
		if generated.GetType() != declared.GetType() {
			return false
		}
		if declared.GetTypeName() == "" {
			return generated.GetTypeName() == ""
		}
		return local.resolves(generated.GetTypeName(), declared.GetTypeName(), scope)
	}
	switch generated.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_MESSAGE,
		descriptorpb.FieldDescriptorProto_TYPE_ENUM,
		descriptorpb.FieldDescriptorProto_TYPE_GROUP:
	default:
		return false
	}
	return local.resolves(generated.GetTypeName(), declared.GetTypeName(), scope)
}

func enumsEquivalent(generated, declared []*descriptorpb.EnumDescriptorProto) bool {
	if len(generated) != len(declared) {
		return false
	}
	for index, enum := range generated {
		other := declared[index]
		if enum.GetName() != other.GetName() || len(enum.GetValue()) != len(other.GetValue()) {
			return false
		}
		for valueIndex, value := range enum.GetValue() {
			otherValue := other.GetValue()[valueIndex]
			if value.GetName() != otherValue.GetName() || value.GetNumber() != otherValue.GetNumber() {
				return false
			}
		}
	}
	return true
}

// localProtoNames holds the fully qualified names (leading dot) of the
// declared file's types and of its package prefixes.
type localProtoNames struct {
	types map[string]struct{}
	names map[string]struct{}
}

func newLocalProtoNames(file *descriptorpb.FileDescriptorProto) localProtoNames {
	local := localProtoNames{
		types: make(map[string]struct{}),
		names: make(map[string]struct{}),
	}
	scope := ""
	if file.GetPackage() != "" {
		for _, part := range strings.Split(file.GetPackage(), ".") {
			scope += "." + part
			local.names[scope] = struct{}{}
		}
	}
	var walk func(scope string, messages []*descriptorpb.DescriptorProto, enums []*descriptorpb.EnumDescriptorProto)
	walk = func(scope string, messages []*descriptorpb.DescriptorProto, enums []*descriptorpb.EnumDescriptorProto) {
		for _, enum := range enums {
			name := scope + "." + enum.GetName()
			local.types[name], local.names[name] = struct{}{}, struct{}{}
		}
		for _, message := range messages {
			name := scope + "." + message.GetName()
			local.types[name], local.names[name] = struct{}{}, struct{}{}
			walk(name, message.GetNestedType(), message.GetEnumType())
		}
	}
	walk(scope, file.GetMessageType(), file.GetEnumType())
	return local
}

// resolves reports whether the type name written in a .proto, at scope,
// resolves to the generated descriptor's fully qualified name under protobuf
// scoping. The first name component is looked up from the innermost scope
// outward; when it names something this file declares, the reference resolves
// there or nowhere. Otherwise the generated name must be one of the scoped
// candidates and must not be a type this file declares.
func (local localProtoNames) resolves(qualified, written, scope string) bool {
	if qualified == "" || written == "" || !strings.HasPrefix(qualified, ".") {
		return false
	}
	if strings.HasPrefix(written, ".") {
		return qualified == written
	}
	first := written
	if index := strings.Index(written, "."); index >= 0 {
		first = written[:index]
	}
	var candidates []string
	for current := scope; ; {
		if _, declared := local.names[current+"."+first]; declared {
			return qualified == current+"."+written
		}
		candidates = append(candidates, current+"."+written)
		if current == "" {
			break
		}
		current = current[:strings.LastIndex(current, ".")]
	}
	if _, declared := local.types[qualified]; declared {
		return false
	}
	return slices.Contains(candidates, qualified)
}
