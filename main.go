// main.go
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
	"sync"
	"time"
)

const (
	BoardSize = 15 // 棋盘大小
	Empty     = 0  // 空位
	Player1   = 1  // 玩家1 (通常是服务器)
	Player2   = 2  // 玩家2 (通常是客户端)
)

// 消息类型
const (
	MsgTypeMove   = "move"   // 移动棋子
	MsgTypeChat   = "chat"   // 聊天消息
	MsgTypeState  = "state"  // 游戏状态 (轮到谁, 游戏结束等)
	MsgTypeAssign = "assign" // 分配玩家编号
	MsgTypeError  = "error"  // 错误消息
)

// 网络消息结构体
type Message struct {
	Type    string `json:"type"`              // 消息类型
	Player  int    `json:"player"`            // 发送者玩家编号 (1 or 2)
	X       int    `json:"x,omitempty"`       // 移动的 X 坐标
	Y       int    `json:"y,omitempty"`       // 移动的 Y 坐标
	Content string `json:"content,omitempty"` // 聊天内容 或 状态描述 或 错误信息
	Turn    int    `json:"turn,omitempty"`    // 当前轮到谁
	Winner  int    `json:"winner,omitempty"`  // 获胜者 (0: 进行中, 1: Player1, 2: Player2, 3: 平局)
}

// 游戏状态
type GameState struct {
	board         [][]int
	currentPlayer int
	winner        int // 0: 进行中, 1: Player1, 2: Player2, 3: 平局
	gameOver      bool
	mu            sync.Mutex // 用于保护棋盘和游戏状态的并发访问
	conn          net.Conn   // 网络连接
	playerID      int        // 当前实例是玩家1还是玩家2
	encoder       *json.Encoder
	decoder       *json.Decoder
	chatHistory   []string
	chatMu        sync.Mutex // 保护聊天记录
}

// --- 游戏逻辑 ---

// 初始化棋盘
func NewBoard(size int) [][]int {
	board := make([][]int, size)
	for i := range board {
		board[i] = make([]int, size)
	}
	return board
}

// 打印棋盘到控制台
func (gs *GameState) DisplayBoard() {
	fmt.Print("\n   ") // 列号上方留空
	for j := 0; j < BoardSize; j++ {
		fmt.Printf("%2d ", j)
	}
	fmt.Println()
	fmt.Print("  +-")
	for j := 0; j < BoardSize; j++ {
		fmt.Print("--+")
	}
	fmt.Println()

	for i := 0; i < BoardSize; i++ {
		fmt.Printf("%2d|", i) // 行号
		for j := 0; j < BoardSize; j++ {
			switch gs.board[i][j] {
			case Empty:
				fmt.Print(" . ")
			case Player1:
				fmt.Print(" X ") // 玩家1 使用 X
			case Player2:
				fmt.Print(" O ") // 玩家2 使用 O
			}
			// fmt.Print("|") // 如果需要格子线
		}
		fmt.Printf("|%d\n", i) // 行号
		// fmt.Print("  +-")
		// for j := 0; j < BoardSize; j++ {
		// 	fmt.Print("--+")
		// }
		// fmt.Println()
	}
	fmt.Print("  +-")
	for j := 0; j < BoardSize; j++ {
		fmt.Print("--+")
	}
	fmt.Println()
	fmt.Print("   ") // 列号下方
	for j := 0; j < BoardSize; j++ {
		fmt.Printf("%2d ", j)
	}
	fmt.Println("\n")
}

// 检查是否获胜
func (gs *GameState) CheckWin(player int) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	return checkWinLogic(gs.board, player)
}

// 检查获胜的核心逻辑 (无锁)
func checkWinLogic(board [][]int, player int) bool {
	size := len(board)

	// 检查行、列、对角线是否有五子连珠
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			if board[i][j] == player {
				// 检查水平方向
				if j+4 < size &&
					board[i][j+1] == player &&
					board[i][j+2] == player &&
					board[i][j+3] == player &&
					board[i][j+4] == player {
					return true
				}
				// 检查垂直方向
				if i+4 < size &&
					board[i+1][j] == player &&
					board[i+2][j] == player &&
					board[i+3][j] == player &&
					board[i+4][j] == player {
					return true
				}
				// 检查主对角线 (\)
				if i+4 < size && j+4 < size &&
					board[i+1][j+1] == player &&
					board[i+2][j+2] == player &&
					board[i+3][j+3] == player &&
					board[i+4][j+4] == player {
					return true
				}
				// 检查副对角线 (/)
				if i+4 < size && j-4 >= 0 &&
					board[i+1][j-1] == player &&
					board[i+2][j-2] == player &&
					board[i+3][j-3] == player &&
					board[i+4][j-4] == player {
					return true
				}
			}
		}
	}
	return false
}

// 检查是否平局 (棋盘满了)
func (gs *GameState) CheckDraw() bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	return checkDrawLogic(gs.board)
}

// 检查平局的核心逻辑 (无锁)
func checkDrawLogic(board [][]int) bool {
	for i := 0; i < len(board); i++ {
		for j := 0; j < len(board[i]); j++ {
			if board[i][j] == Empty {
				return false // 还有空位，不是平局
			}
		}
	}
	return true // 棋盘已满
}

// 尝试落子
func (gs *GameState) PlacePiece(x, y, player int) bool {
	gs.mu.Lock()
	defer gs.mu.Unlock()
	if x < 0 || x >= BoardSize || y < 0 || y >= BoardSize || gs.board[x][y] != Empty {
		return false // 无效位置或已有棋子
	}
	gs.board[x][y] = player
	return true
}

// 添加聊天消息
func (gs *GameState) AddChatMessage(sender string, message string) {
	gs.chatMu.Lock()
	defer gs.chatMu.Unlock()
	gs.chatHistory = append(gs.chatHistory, fmt.Sprintf("[%s]: %s", sender, message))
	// 可以限制聊天记录的长度
	const maxChatHistory = 20
	if len(gs.chatHistory) > maxChatHistory {
		gs.chatHistory = gs.chatHistory[len(gs.chatHistory)-maxChatHistory:]
	}
}

// 显示聊天记录
func (gs *GameState) DisplayChat() {
	gs.chatMu.Lock()
	defer gs.chatMu.Unlock()
	fmt.Println("--- Chat ---")
	if len(gs.chatHistory) == 0 {
		fmt.Println("(No messages yet)")
	} else {
		for _, msg := range gs.chatHistory {
			fmt.Println(msg)
		}
	}
	fmt.Println("------------")
}

// --- 网络处理 ---

// 发送消息
func (gs *GameState) SendMessage(msg Message) error {
	// 发送消息前确保连接存在
	if gs.conn == nil {
		return fmt.Errorf("no connection established")
	}
	// log.Printf("DEBUG: Sending message: %+v\n", msg) // 调试日志
	err := gs.encoder.Encode(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
		// 考虑在这里处理连接断开的情况
		gs.gameOver = true // 假设连接错误导致游戏结束
	}
	return err
}

// 接收消息并处理 (在一个单独的 goroutine 中运行)
func (gs *GameState) ReceiveMessages() {
	if gs.conn == nil {
		log.Println("Cannot receive messages: no connection")
		return
	}

	for {
		var msg Message
		err := gs.decoder.Decode(&msg)
		if err != nil {
			// log.Printf("Error receiving message: %v. Opponent likely disconnected.", err)
			fmt.Println("\nConnection lost. Opponent may have disconnected.")
			gs.mu.Lock()
			gs.gameOver = true // 标记游戏结束
			gs.mu.Unlock()
			gs.conn.Close() // 关闭连接
			// 通知主循环退出 (例如通过 channel)
			// close(someChannel) // 如果使用了 channel
			os.Exit(1) // 或者直接退出
			return
		}

		// log.Printf("DEBUG: Received message: %+v\n", msg) // 调试日志

		gs.mu.Lock() // 加锁保护状态修改
		switch msg.Type {
		case MsgTypeMove:
			if msg.Player != gs.playerID && gs.currentPlayer == msg.Player {
				// 对方的移动
				if gs.PlacePiece(msg.X, msg.Y, msg.Player) {
					fmt.Printf("\nOpponent (Player %d) placed at (%d, %d)\n", msg.Player, msg.X, msg.Y)
					gs.DisplayBoard() // 更新棋盘显示

					// 检查对方是否获胜或平局
					if checkWinLogic(gs.board, msg.Player) {
						gs.winner = msg.Player
						gs.gameOver = true
						fmt.Printf("\nPlayer %d wins!\n", msg.Player)
					} else if checkDrawLogic(gs.board) {
						gs.winner = 3 // 平局
						gs.gameOver = true
						fmt.Println("\nIt's a draw!")
					} else {
						gs.currentPlayer = gs.playerID // 轮到自己
					}
				} else {
					log.Printf("Received invalid move from opponent: (%d, %d)", msg.X, msg.Y)
					// 可以选择发送错误消息回去，或者断开连接
					gs.SendMessage(Message{Type: MsgTypeError, Content: "Received invalid move"})
				}
			} else if msg.Player == gs.playerID {
				// 忽略自己发送的移动回显（理论上不应该收到）
			} else {
				log.Printf("WARN: Received move from player %d, but current turn is %d", msg.Player, gs.currentPlayer)
				// 忽略非当前玩家的移动消息
			}
		case MsgTypeChat:
			senderName := fmt.Sprintf("Player %d", msg.Player)
			if msg.Player == gs.playerID {
				senderName = "You" // 可以显示为 "You"
			}
			gs.AddChatMessage(senderName, msg.Content)
			// 在主循环中或其他地方定期显示聊天记录，或者立即显示
			fmt.Printf("\n[%s]: %s\n", senderName, msg.Content)
			// 提示用户继续输入
			fmt.Printf("Your turn (Player %d). Enter move (x,y) or chat (/c message): ", gs.playerID)

		case MsgTypeState:
			// 通常由服务器发送给客户端，更新游戏状态
			gs.currentPlayer = msg.Turn
			gs.winner = msg.Winner
			gs.gameOver = (msg.Winner != 0)
			if gs.gameOver {
				fmt.Println("\nGame Over received from remote.")
				if msg.Winner == 3 {
					fmt.Println("It's a draw!")
				} else if msg.Winner != 0 {
					fmt.Printf("Player %d wins!\n", msg.Winner)
				}
			}
			// 可能还需要更新棋盘状态（如果状态消息包含棋盘）
		case MsgTypeAssign:
			// 客户端接收服务器分配的玩家编号
			if gs.playerID == 0 { // 确保只分配一次
				gs.playerID = msg.Player
				fmt.Printf("You are Player %d\n", gs.playerID)
				// 如果自己是玩家2，通常开始时是玩家1先走
				if gs.playerID == Player2 {
					gs.currentPlayer = Player1
					fmt.Println("Waiting for Player 1's move...")
				} else {
					gs.currentPlayer = Player1                         // 服务器（玩家1）总是先开始
					fmt.Printf("Waiting for Player 2 to connect...\n") // 服务器等待客户端连接后的消息
				}
			}
		case MsgTypeError:
			log.Printf("Received error from opponent: %s", msg.Content)
			// 可以根据错误类型决定是否结束游戏
			// fmt.Println("\nReceived error:", msg.Content)
			// gs.gameOver = true // 例如，某些错误可能导致游戏结束

		default:
			log.Printf("Received unknown message type: %s", msg.Type)
		}
		gs.mu.Unlock() // 解锁

		// 如果游戏结束，可以退出接收循环
		if gs.gameOver {
			// fmt.Println("Receive loop ending due to game over.") // Debug
			return
		}
	}
}

// --- 主程序逻辑 ---

func main() {
	listenAddr := flag.String("listen", "", "Address to listen on (e.g., :8080) to run as server")
	connectAddr := flag.String("connect", "", "Address to connect to (e.g., localhost:8080) to run as client")
	flag.Parse()

	gs := &GameState{
		board:         NewBoard(BoardSize),
		currentPlayer: Player1, // 默认玩家1先手
		winner:        0,
		gameOver:      false,
		playerID:      0, // 初始未知
		chatHistory:   make([]string, 0),
	}

	var listener net.Listener
	var err error

	// 根据命令行参数决定作为服务器还是客户端
	if *listenAddr != "" {
		// --- 服务器模式 ---
		fmt.Println("Starting server on", *listenAddr)
		listener, err = net.Listen("tcp", *listenAddr)
		if err != nil {
			log.Fatalf("Failed to listen: %v", err)
		}
		defer listener.Close()

		fmt.Println("Waiting for opponent to connect...")
		conn, err := listener.Accept()
		if err != nil {
			log.Fatalf("Failed to accept connection: %v", err)
		}
		fmt.Println("Opponent connected from", conn.RemoteAddr())
		gs.conn = conn        // 保存连接
		gs.playerID = Player1 // 服务器是玩家1
		gs.encoder = json.NewEncoder(conn)
		gs.decoder = json.NewDecoder(conn)

		// 向客户端发送分配信息
		err = gs.SendMessage(Message{Type: MsgTypeAssign, Player: Player2})
		if err != nil {
			log.Printf("Failed to send assign message: %v", err)
			return // or handle error appropriately
		}
		fmt.Println("Assigned Player 2 to the client.")
		gs.currentPlayer = Player1 // 确认服务器先手

	} else if *connectAddr != "" {
		// --- 客户端模式 ---
		fmt.Println("Connecting to server at", *connectAddr)
		conn, err := net.DialTimeout("tcp", *connectAddr, 10*time.Second) // 增加超时
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		fmt.Println("Connected to server.")
		gs.conn = conn // 保存连接
		gs.encoder = json.NewEncoder(conn)
		gs.decoder = json.NewDecoder(conn)
		// 客户端需要等待服务器分配 PlayerID (在 ReceiveMessages 中处理)
		fmt.Println("Waiting for player assignment from server...")

	} else {
		fmt.Println("Please specify either --listen <addr> or --connect <addr>")
		os.Exit(1)
	}
	defer gs.conn.Close() // 确保连接最终关闭

	// 启动一个 goroutine 持续接收和处理来自对方的消息
	go gs.ReceiveMessages()

	// 等待玩家ID分配完成 (尤其是客户端)
	for gs.playerID == 0 {
		time.Sleep(100 * time.Millisecond) // 等待分配消息
		gs.mu.Lock()
		gameOverCheck := gs.gameOver // 检查是否在等待期间连接就断了
		gs.mu.Unlock()
		if gameOverCheck {
			fmt.Println("Connection closed while waiting for player assignment.")
			return
		}
	}

	// --- 主游戏循环 ---
	reader := bufio.NewReader(os.Stdin)
	for {
		gs.mu.Lock() // 读取状态前加锁
		if gs.gameOver {
			gs.mu.Unlock() // 检查完 gameOver 后解锁
			break
		}
		currentTurnPlayer := gs.currentPlayer
		myTurn := (currentTurnPlayer == gs.playerID)
		myPlayerID := gs.playerID
		gs.mu.Unlock() // 读取完状态后解锁

		// 显示棋盘和聊天记录
		gs.DisplayBoard()
		gs.DisplayChat()

		if myTurn {
			fmt.Printf("Your turn (Player %d). Enter move (x,y) or chat (/c message): ", myPlayerID)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)

			if strings.HasPrefix(input, "/c ") {
				// --- 处理聊天输入 ---
				chatMsg := strings.TrimPrefix(input, "/c ")
				if chatMsg != "" {
					// 1. 添加到本地聊天记录
					gs.AddChatMessage(fmt.Sprintf("You (Player %d)", myPlayerID), chatMsg)
					// 2. 发送给对方
					sendErr := gs.SendMessage(Message{
						Type:    MsgTypeChat,
						Player:  myPlayerID,
						Content: chatMsg,
					})
					if sendErr != nil {
						fmt.Println("Failed to send chat message:", sendErr)
						// 可以在这里决定是否因发送失败而结束游戏
					}
					// 显示更新后的聊天记录
					// gs.DisplayChat() // DisplayBoard() 之后会显示，避免重复
				}
			} else {
				// --- 处理移动输入 ---
				parts := strings.Split(input, ",")
				if len(parts) == 2 {
					xStr, yStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
					x, errX := strconv.Atoi(xStr)
					y, errY := strconv.Atoi(yStr)

					if errX == nil && errY == nil {
						gs.mu.Lock() // 修改棋盘前加锁
						validMove := gs.PlacePiece(x, y, myPlayerID)
						gs.mu.Unlock() // 修改棋盘后解锁

						if validMove {
							fmt.Printf("You placed at (%d, %d)\n", x, y)
							// 发送移动给对方
							sendErr := gs.SendMessage(Message{
								Type:   MsgTypeMove,
								Player: myPlayerID,
								X:      x,
								Y:      y,
							})
							if sendErr != nil {
								fmt.Println("Failed to send move:", sendErr)
								gs.mu.Lock()
								gs.gameOver = true // 发送失败，游戏结束
								gs.mu.Unlock()
								continue // 跳过胜负检查，直接进入下一轮循环检查 gameOver
							}

							// 检查自己是否获胜或平局
							gs.mu.Lock() // 检查胜负前加锁
							if checkWinLogic(gs.board, myPlayerID) {
								gs.winner = myPlayerID
								gs.gameOver = true
								gs.DisplayBoard() // 显示最终棋盘
								fmt.Println("\nCongratulations! You win!")
								// 可以选择发送最终状态给对方
								gs.SendMessage(Message{Type: MsgTypeState, Winner: myPlayerID, Turn: 0})
							} else if checkDrawLogic(gs.board) {
								gs.winner = 3 // 平局
								gs.gameOver = true
								gs.DisplayBoard() // 显示最终棋盘
								fmt.Println("\nIt's a draw!")
								// 发送平局状态
								gs.SendMessage(Message{Type: MsgTypeState, Winner: 3, Turn: 0})
							} else {
								// 切换回合
								gs.currentPlayer = 3 - myPlayerID // 切换到另一个玩家
								fmt.Printf("Waiting for Player %d's move...\n", gs.currentPlayer)
							}
							gs.mu.Unlock() // 检查胜负后解锁

						} else {
							fmt.Println("Invalid move. Try again.")
						}
					} else {
						fmt.Println("Invalid input format. Use x,y for moves (e.g., 7,7) or /c <message> for chat.")
					}
				} else {
					fmt.Println("Invalid input format. Use x,y for moves (e.g., 7,7) or /c <message> for chat.")
				}
			}
		} else {
			// 不是我的回合，等待对方移动 (ReceiveMessages goroutine 会处理)
			// fmt.Printf("Waiting for Player %d's move...\n", currentTurnPlayer) // 可以在ReceiveMessages中打印提示
			time.Sleep(500 * time.Millisecond) // 短暂休眠避免CPU空转
		}

		// 短暂休眠，避免在对方回合时过于频繁地检查状态和重绘屏幕
		if !myTurn {
			time.Sleep(200 * time.Millisecond)
		}

	} // end game loop

	fmt.Println("Game Over.")
	// 可以在这里添加最终的胜负信息显示
	gs.mu.Lock()
	winner := gs.winner
	gs.mu.Unlock()
	switch winner {
	case Player1:
		fmt.Println("Player 1 won!")
	case Player2:
		fmt.Println("Player 2 won!")
	case 3:
		fmt.Println("The game is a draw!")
	default:
		// 可能因为连接断开等原因结束
		fmt.Println("Game ended unexpectedly.")

	}
	// 等待用户按 Enter 键退出，以便查看最终结果
	fmt.Println("Press Enter to exit.")
	reader.ReadString('\n')
}
