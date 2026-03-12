package filer

import (
	"embed"
	"sync"

	"github.com/google/uuid"
)

//go:embed *.proto
var ProtoFiles embed.FS

// TODO iterate over ProtoFiles and:
// 1. fill user_types with ("system", map<filename, filedescriptorproto>)
// 2. fill user_protos with ("system/typename", descriptorproto)
// 3. fill user_rpc with ("system/service", servicedescriptorproto) and ("system/service.method", methoddescriptorproto)
// 4. fill shared_types with all system FileDescriptorProtos merged into one
var user_proto sync.Map
var user_service sync.Map
var user_rpc sync.Map

var server_id = "default"

func init() {
	// replace with standard library uuid when ready https://github.com/golang/go/issues/62026
	server_id = uuid.New().String()
}
