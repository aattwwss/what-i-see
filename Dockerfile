FROM golang:1.27-alpine AS build

WORKDIR /app

COPY go.* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /main

FROM alpine:3.24

COPY --from=build /main /main

USER nobody:nogroup

CMD [ "/main" ]
