
# 生成grpc文件
protoc -I $GOPATH/src  -I . --go_out=.  --go-grpc_out=.  hello.proto

# 生成micro文件
protoc -I $GOPATH/src  -I .  --micro_out=. --go_out=.  --go-grpc_out=.  hello.proto

# 生成gw.pb文件
protoc -I $GOPATH/src  -I .  --micro_out=. --go_out=.  --go-grpc_out=.  --grpc-gateway_out=logtostderr=true,register_func_suffix=Gw:. hello.proto
