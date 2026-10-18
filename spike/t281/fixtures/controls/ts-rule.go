package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
c.Do(ctx,"TS.CREATERULE","temperature","temperature:avg","AGGREGATION","avg",60000)
}
