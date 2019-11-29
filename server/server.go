package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"runtime"
	"time"
)

var (
	localPort  int
	remotePort int
)

func init() {
	flag.IntVar(&localPort, "l", 5200, "the user link port")
	flag.IntVar(&remotePort, "r", 3333, "client listen port")
}

type client struct {
	conn net.Conn
	// 数据传输通道
	read  chan []byte
	write chan []byte
	// 异常退出通道
	exit  chan error
	close chan error
}

// 从Client端读取数据
func (c *client) Read() {
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(time.Second * 60))
		data := make([]byte, 10240)
		n, err := c.conn.Read(data)

		if err != nil && err != io.EOF {
			fmt.Println("读取出现错误...")
			c.exit <- err
			c.close <- err
		}

		// 收到心跳包，原样返回
		if data[0] == 'p' && data[1] == 'i' {
			c.conn.Write([]byte("pi"))
			continue
		}
		c.read <- data[:n]
	}
}

// 将数据写入到Client端
func (c *client) Write() {
	for {
		select {
		case data := <-c.write:
			_, err := c.conn.Write(data)
			if err != nil && err != io.EOF {
				c.exit <- err
				c.close <- err
			}
		}
	}
}

type user struct {
	conn net.Conn
	// 数据传输通道
	read  chan []byte
	write chan []byte
	// 异常退出通道
	exit  chan error
	close chan error
}

// 从User端读取数据
func (u *user) Read() {
	_ = u.conn.SetReadDeadline(time.Now().Add(time.Second * 200))
	for {
		data := make([]byte, 10240)
		n, err := u.conn.Read(data)
		if err != nil && err != io.EOF {
			u.exit <- err
			u.close <- err
		}
		u.read <- data[:n]
	}
}

// 将数据写给User端
func (u *user) Write() {
	for {
		select {
		case data := <-u.write:
			_, err := u.conn.Write(data)
			if err != nil && err != io.EOF {
				u.exit <- err
				u.close <- err
			}
		}
	}
}

func main() {
	flag.Parse()

	defer func() {
		err := recover()
		if err != nil {
			fmt.Println(err)
		}
	}()

	clientListener, err := net.Listen("tcp", fmt.Sprintf(":%d", remotePort))
	if err != nil {
		panic(err)
	}
	fmt.Printf("监听:%d端口, 等待client连接... \n", remotePort)

	for {
		// 有Client来连接了
		clientConn, err := clientListener.Accept()
		if err != nil {
			panic(err)
		}

		fmt.Printf("有Client连接: %s \n", clientConn.RemoteAddr())

		client := &client{
			conn:  clientConn,
			read:  make(chan []byte),
			write: make(chan []byte),
			exit:  make(chan error),
			close: make(chan error),
		}

		next := make(chan bool)
		go Lister(client, next)

		<-next
		fmt.Println("重新等待新的client连接..")
	}
}

func Lister(client *client, next chan bool) {
	// 监听User来连接
	userListener, err := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
	if err != nil {
		panic(err)
	}
	fmt.Printf("监听:%d端口, 等待user连接.... \n", localPort)

	userConnChan := make(chan net.Conn)
	go AcceptUserConn(userListener, userConnChan)

	go client.Read()
	go client.Write()

	exit := false

	for !exit {
		select {
		case err := <-client.exit:
			fmt.Printf("client出现错误, 开始重试, err: %s \n", err.Error())
			next <- true
			exit = true

		case userConn := <-userConnChan:
			user := &user{
				conn:  userConn,
				read:  make(chan []byte),
				write: make(chan []byte),
				exit:  make(chan error),
				close: make(chan error),
			}
			go user.Read()
			go user.Write()

			go handle(client, user)
		}
	}

	runtime.Goexit()
}

// 将两个Socket通道链接
// 1. 将从user收到的信息发给client
// 2. 将从client收到信息发给user
func handle(client *client, user *user) {
	for {
		select {
		case userRecv := <-user.read:
			// 收到从user发来的信息
			client.write <- userRecv
		case clientRecv := <-client.read:
			//fmt.Println("收到从client发来的信息")
			user.write <- clientRecv

		case err := <-client.close:
			fmt.Println("client出现错误，关闭连接", err.Error())
			_ = client.conn.Close()
			_ = user.conn.Close()
			// 结束当前goroutine
			runtime.Goexit()

		case err := <-user.close:
			fmt.Println("user出现错误，关闭连接", err.Error())
			_ = user.conn.Close()
			_ = client.conn.Close()
			runtime.Goexit()
		}
	}
}

// 等待user连接
func AcceptUserConn(userListener net.Listener, connChan chan net.Conn) {
	userConn, err := userListener.Accept()
	if err != nil {
		panic(err)
	}
	fmt.Printf("user connect: %s \n", userConn.RemoteAddr())
	connChan <- userConn
}