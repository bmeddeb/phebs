package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
c.Eval(ctx,"return 1",[]string{"script:one","script:two"})
}
