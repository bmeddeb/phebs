package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
c.Do(ctx,"FT.CREATE","idxA","ON","HASH","PREFIX",1,"doc:","SCHEMA","name","TEXT")
c.Do(ctx,"FT.CREATE","idxB","ON","HASH","PREFIX",1,"doc:","SCHEMA","name","TEXT")
c.Get(ctx,"doc:key")
}
