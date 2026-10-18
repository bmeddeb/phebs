package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
c.Do(ctx,"FT.CREATE","index","ON","HASH","SCHEMA","v","VECTOR","FLAT",99,"TYPE","FLOAT32")
}
