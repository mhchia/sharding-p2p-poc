
all: $(pb_go)

$(pb_go): $(pb_target)
	protoc -I . $(pb_target) --go_out=plugins=grpc:.

test:
	echo $(OBJECTS)
	a=$(shell find . -type d -maxdepth 1|sed '/^\.$$/d')

clean:
	rm $(pb_go)
