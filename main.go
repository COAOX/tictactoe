package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
)

// Move 定义用于网络传输的移动结构
type Move struct {
	Player string
	Row    int
	Col    int
	Error  string    // 用于传输错误信息
	Status GameState // 用于传输游戏状态
	Winner string    // 用于传输获胜者
}

func main() {
	listenAddr := flag.String("listen", "", "作为服务器监听的地址 (例如 :8080)")
	connectAddr := flag.String("connect", "", "作为客户端连接的服务器地址 (例如 localhost:8080)")
	flag.Parse()

	if *listenAddr != "" && *connectAddr != "" {
		log.Fatal("不能同时指定 -listen 和 -connect")
	}

	if *listenAddr != "" {
		runServer(*listenAddr)
	} else if *connectAddr != "" {
		runClient(*connectAddr)
	} else {
		fmt.Println("请使用 -listen <地址:端口> 启动服务器或使用 -connect <地址:端口> 连接到服务器")
		fmt.Println("示例:")
		fmt.Println("  服务器: go run . -listen :8080")
		fmt.Println("  客户端: go run . -connect localhost:8080")
		// 这里可以添加一个本地双人对战的选项，如果需要的话
	}
}

// runServer 运行服务器逻辑
func runServer(addr string) {
	fmt.Printf("服务器模式: 等待客户端连接 %s ...\n", addr)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("监听失败: %v", err)
	}
	defer listener.Close()

	conn, err := listener.Accept() // 等待一个客户端连接
	if err != nil {
		log.Fatalf("接受连接失败: %v", err)
	}
	defer conn.Close()
	fmt.Println("客户端已连接!")

	game := NewGame()
	player := PlayerX // 服务器是 X
	opponent := PlayerO

	handleGame(conn, game, player, opponent)
}

// runClient 运行客户端逻辑
func runClient(addr string) {
	fmt.Printf("客户端模式: 正在连接到服务器 %s ...\n", addr)
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatalf("连接服务器失败: %v", err)
	}
	defer conn.Close()
	fmt.Println("已连接到服务器!")

	game := NewGame()
	player := PlayerO // 客户端是 O
	opponent := PlayerX

	handleGame(conn, game, player, opponent)
}

// handleGame 处理游戏循环和网络通信
func handleGame(conn net.Conn, game *TicTacToe, player, opponent string) {
	reader := bufio.NewReader(conn)    // 用于读取网络数据
	writer := bufio.NewWriter(conn)    // 用于写入网络数据
	encoder := json.NewEncoder(writer) // JSON 编码器
	decoder := json.NewDecoder(reader) // JSON 解码器

	fmt.Printf("你是玩家 %s\n", player)

	for game.GetState() == StatePlaying {
		displayBoard(game.GetBoard())

		var move Move
		var err error

		if game.GetCurrentPlayer() == player {
			// 轮到本地玩家
			fmt.Printf("轮到你 (%s) 下棋了。\n", player)
			row, col := getPlayerInput(player)

			err = game.MakeMove(row, col)
			if err != nil {
				fmt.Printf("无效移动: %v。请重试。\n", err)
				continue // 让玩家重新输入
			}

			// 准备要发送的移动数据
			move = Move{
				Player: player,
				Row:    row,
				Col:    col,
				Status: game.GetState(),  // 发送最新的游戏状态
				Winner: game.GetWinner(), // 发送获胜者信息
			}

			// 发送移动给对手
			if err := encoder.Encode(&move); err != nil {
				log.Printf("发送移动失败: %v", err)
				return // 网络错误，结束游戏
			}
			if err := writer.Flush(); err != nil {
				log.Printf("刷新写入缓冲区失败: %v", err)
				return
			}
			fmt.Println("等待对手...")

		} else {
			// 轮到对手玩家
			fmt.Printf("等待对手 (%s) 下棋...\n", opponent)

			// 接收对手的移动
			if err := decoder.Decode(&move); err != nil {
				// 检查是否是连接断开的错误
				if err.Error() == "EOF" {
					fmt.Println("对手已断开连接。")
				} else {
					log.Printf("接收移动失败: %v", err)
				}
				return // 网络错误或对手断开，结束游戏
			}

			// 检查对手是否发送了错误信息
			if move.Error != "" {
				fmt.Printf("收到错误信息: %s\n", move.Error)
				// 这里可以根据需要处理错误，例如结束游戏
				return
			}

			// 在本地游戏状态中应用对手的移动
			// 注意：我们信任对手发送的移动是合法的，因为服务器/客户端代码是对称的
			// 更健壮的实现会再次验证接收到的移动
			if move.Player != opponent {
				log.Printf("收到非预期玩家 (%s) 的移动，期望的是 %s", move.Player, opponent)
				// 可以选择忽略或结束游戏
				continue
			}

			err = game.MakeMove(move.Row, move.Col)
			if err != nil {
				// 如果对手发送了一个无效的移动（理论上不应发生，除非代码不同步或被篡改）
				log.Printf("错误: 对手发送了无效的移动 (%d, %d): %v", move.Row+1, move.Col+1, err)
				// 可以向对方发送错误信息
				errMsg := Move{Error: fmt.Sprintf("你发送了无效移动 (%d, %d)", move.Row+1, move.Col+1)}
				encoder.Encode(&errMsg)
				writer.Flush()
				return // 结束游戏
			}
			// 更新本地游戏状态（如果对手的移动导致游戏结束）
			// MakeMove 内部已经更新了状态，这里不需要再根据 move.Status 更新
		}
	}

	// 游戏结束
	displayBoard(game.GetBoard())
	switch game.GetState() {
	case StateWon:
		if game.GetWinner() == player {
			fmt.Println("恭喜！你赢了！")
		} else {
			fmt.Printf("很遗憾，你输了。玩家 %s 获胜。\n", game.GetWinner())
		}
	case StateDraw:
		fmt.Println("游戏平局！")
	}
}

// getPlayerInput 从终端获取玩家输入 (行和列)
func getPlayerInput(player string) (int, int) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("玩家 %s, 请输入你的移动 (格式: 行,列 或 行 列，例如 1,2 或 1 2): ", player)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		// 尝试用逗号或空格分割
		parts := strings.FieldsFunc(input, func(r rune) bool {
			return r == ',' || r == ' '
		})

		if len(parts) != 2 {
			fmt.Println("输入格式错误，请使用 '行,列' 或 '行 列' 格式。")
			continue
		}

		rowStr, colStr := parts[0], parts[1]
		row, errRow := strconv.Atoi(rowStr)
		col, errCol := strconv.Atoi(colStr)

		if errRow != nil || errCol != nil || row < 1 || row > 3 || col < 1 || col > 3 {
			fmt.Println("无效的数字或范围，行和列都必须是 1 到 3 之间的数字。")
			continue
		}

		// 将用户输入的 1-based 索引转换为 0-based 索引
		return row - 1, col - 1
	}
}
