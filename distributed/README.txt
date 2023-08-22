client:
It includes the controller in the local machine. It also includes the test files and SDL programs.
The host_ip in the distributor function in distributor.go should be replaced using public ip of the AWS node employing the Gof engine.
aws_server:
It is the Gof engine. The code should be placed on the AWS macine and then run it.
Go to the aws_server directory and then use `go run .` to run the GOf engine.
The port I used is 8030.
