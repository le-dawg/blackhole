package dnsd
import "context"
func init() {
	StartProcessMonitor(context.Background())
}
