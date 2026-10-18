package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
{ redis:=shadowFactory(); rc:=redis.NewClient(nil); rc.Get(ctx,"k") }
}
