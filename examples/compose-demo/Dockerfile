# The image of the service under test: the failover script of the shell
# example, with what it needs. Built from the examples directory:
#   docker build -f compose-demo/Dockerfile ..
FROM alpine:3.22
RUN apk add --no-cache bash curl iputils
WORKDIR /app
COPY shell-demo/failover.sh shell-demo/test.sh ./
