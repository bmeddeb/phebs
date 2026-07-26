// A parse-only migration adversary: both Sarama eras are imported under
// distinct aliases, so receiver-untyped method recognition must abstain
// instead of choosing an import by map iteration.
package synthetic

import (
	"context"

	ibm "github.com/IBM/sarama"
	shopify "github.com/Shopify/sarama"
)

func ambiguousConsume(
	ctx context.Context,
	group interface {
		Consume(context.Context, []string, any) error
	},
	handler any,
	_ shopify.ConsumerGroup,
	_ ibm.ConsumerGroup,
) error {
	return group.Consume(ctx, []string{"orders-v1"}, handler)
}
