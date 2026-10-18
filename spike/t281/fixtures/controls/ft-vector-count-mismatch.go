package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
c.Do(ctx,"FT.CREATE","index","ON","HASH","PREFIX",1,"doc:","SCHEMA","v","VECTOR","FLAT",4,"TYPE","FLOAT32","DIM",3,"DISTANCE_METRIC","COSINE")
}
