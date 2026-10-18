package neutral
import ("context"; "fmt"; redis "github.com/redis/go-redis/v9")
func example(ctx context.Context,c *redis.Client,key,tenant string,id int) {
args:=[]interface{}{"SET","s:key","value",option()}
c.Do(ctx,args...)
}
