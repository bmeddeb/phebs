package consumer

import health "example.invalid/t225-thrift-field-demo/generated"

func ReadHealth(result *health.Meta_Health_Result) string {
	return result.GetSuccess()
}
