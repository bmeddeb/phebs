package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
type Holder struct { Client *redis.Client }
type Holder struct { Client *OtherClient }
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
var h Holder; h.Client.Get(ctx,"k")
}
