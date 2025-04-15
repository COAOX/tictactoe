// main.go
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io" // 需要导入 io 包处理 EOF
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
	MsgTypeNotify = "notify" // 通用通知 (例如对方已移动)
)

// 网络消息结构体
type Message struct {
	Type    string `json:"type"`              // 消息类型
	Player  int    `json:"player"`            // 发送者玩家编号 (1 or 2)
	X       int    `json:"x,omitempty"`       // 移动的 X 坐标
	Y       int    `json:"y,omitempty"`       // 移动的 Y 坐标
	Content string `json:"content,omitempty"` // 聊天内容 或 状态描述 或 错误信息 或通知
	Turn    int    `json:"turn,omitempty"`    // 当前轮到谁
	Winner  int    `json:"winner,omitempty"`  // 获胜者 (0: 进行中, 1: Player1, 2: Player2, 3: 平局)
}

// 游戏状态
type GameState struct {
	board          [][]int
	currentPlayer  int
	winner         int // 0: 进行中, 1: Player1, 2: Player2, 3: 平局
	gameOver       bool
	mu             sync.Mutex // 用于保护棋盘和游戏状态的并发访问
	conn           net.Conn   // 网络连接
	playerID       int        // 当前实例是玩家1还是玩家2
	encoder        *json.Encoder
	decoder        *json.Decoder
	chatHistory    []string
	chatMu         sync.Mutex    // 保护聊天记录
	needsRedraw    bool          // 标记是否需要重新绘制屏幕
	redrawMu       sync.Mutex    // 保护 needsRedraw
	inputChan      chan string   // 用于从标准输入读取
	networkMsgChan chan Message  // 用于从网络读取
	quitChan       chan struct{} // 用于通知goroutine退出
}

// 设置需要重绘的标志
func (gs *GameState) SetNeedsRedraw() {
	gs.redrawMu.Lock()
	gs.needsRedraw = true
	gs.redrawMu.Unlock()
}

// 检查并重置需要重绘的标志
func (gs *GameState) CheckAndResetRedraw() bool {
	gs.redrawMu.Lock()
	defer gs.redrawMu.Unlock()
	needs := gs.needsRedraw
	gs.needsRedraw = false
	return needs
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

// 打印棋盘到控制台 (需要加锁)
func (gs *GameState) DisplayBoard() {
	gs.mu.Lock()
	defer gs.mu.Unlock() // 确保函数退出时解锁

	// 清屏 (简单的实现，可能在不同终端效果不同)
	// fmt.Print("\033[H\033[2J") // ANSI 清屏序列

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
		}
		fmt.Printf("|%d\n", i) // 行号
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

// 检查是否获胜 (无锁的核心逻辑)
func checkWinLogic(board [][]int, player int) bool {
	size := len(board)
	for i := 0; i < size; i++ {
		for j := 0; j < size; j++ {
			if board[i][j] == player {
				// 水平, 垂直, 对角线检查 (同前)
				if j+4 < size && board[i][j+1] == player && board[i][j+2] == player && board[i][j+3] == player && board[i][j+4] == player {
					return true
				}
				if i+4 < size && board[i+1][j] == player && board[i+2][j] == player && board[i+3][j] == player && board[i+4][j] == player {
					return true
				}
				if i+4 < size && j+4 < size && board[i+1][j+1] == player && board[i+2][j+2] == player && board[i+3][j+3] == player && board[i+4][j+4] == player {
					return true
				}
				if i+4 < size && j-4 >= 0 && board[i+1][j-1] == player && board[i+2][j-2] == player && board[i+3][j-3] == player && board[i+4][j-4] == player {
					return true
				}
			}
		}
	}
	return false
}

// 检查是否平局 (无锁的核心逻辑)
func checkDrawLogic(board [][]int) bool {
	for i := 0; i < len(board); i++ {
		for j := 0; j < len(board[i]); j++ {
			if board[i][j] == Empty {
				return false
			}
		}
	}
	return true
}

// 尝试落子 (需要在外部加锁调用)
func (gs *GameState) placePieceInternal(x, y, player int) bool {
	if x < 0 || x >= BoardSize || y < 0 || y >= BoardSize || gs.board[x][y] != Empty {
		return false
	}
	gs.board[x][y] = player
	return true
}

// 添加聊天消息 (需要加锁)
func (gs *GameState) AddChatMessage(sender string, message string) {
	gs.chatMu.Lock()
	defer gs.chatMu.Unlock()
	gs.chatHistory = append(gs.chatHistory, fmt.Sprintf("[%s]: %s", sender, message))
	const maxChatHistory = 20
	if len(gs.chatHistory) > maxChatHistory {
		gs.chatHistory = gs.chatHistory[len(gs.chatHistory)-maxChatHistory:]
	}
}

// 显示聊天记录 (需要加锁)
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

// 发送消息 (不在锁内调用)
func (gs *GameState) SendMessage(msg Message) error {
	if gs.conn == nil {
		return fmt.Errorf("no connection established")
	}
	// log.Printf("DEBUG: Sending message: %+v\n", msg)
	// 对网络连接的写操作本身应该是线程安全的，但最好还是避免并发写同一个 encoder
	// 如果担心并发写 encoder，可以在这里加一个单独的发送锁
	err := gs.encoder.Encode(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
		// 触发游戏结束流程
		close(gs.quitChan) // 通知其他 goroutine 退出
	}
	return err
}

// Goroutine: 接收网络消息并发送到 channel
func (gs *GameState) networkReceiver() {
	defer func() {
		// 如果接收循环退出（例如连接断开），也通知主循环
		log.Println("Network receiver exiting.")
		select {
		case <-gs.quitChan: // 检查是否已经关闭
		default:
			close(gs.quitChan)
		}
	}()

	if gs.conn == nil {
		log.Println("Cannot receive messages: no connection")
		return
	}

	for {
		select {
		case <-gs.quitChan: // 检查是否需要退出
			log.Println("Network receiver received quit signal.")
			return
		default:
			// 继续尝试读取
		}

		var msg Message
		err := gs.decoder.Decode(&msg)
		if err != nil {
			// 区分 EOF 和其他错误
			if err == io.EOF || strings.Contains(err.Error(), "use of closed network connection") {
				log.Printf("Connection closed by peer or locally.")
			} else {
				log.Printf("Error receiving message: %v.", err)
			}
			// 不论什么错误，都通知退出
			select {
			case <-gs.quitChan:
			default:
				close(gs.quitChan)
			}
			return
		}
		// log.Printf("DEBUG: Received raw message: %+v\n", msg)

		// 发送到 channel，让主循环处理
		select {
		case gs.networkMsgChan <- msg:
			// log.Printf("DEBUG: Sent message to networkMsgChan: %+v\n", msg)
		case <-gs.quitChan:
			log.Println("Network receiver shutting down while sending to channel.")
			return
		}
	}
}

// Goroutine: 从标准输入读取并发送到 channel
func (gs *GameState) inputReader() {
	defer func() {
		log.Println("Input reader exiting.")
		// 如果输入退出（例如Ctrl+D），也通知主循环
		select {
		case <-gs.quitChan:
		default:
			close(gs.quitChan)
		}
	}()
	reader := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-gs.quitChan: // 检查是否需要退出
			log.Println("Input reader received quit signal.")
			return
		default:
			// 继续尝试读取
		}

		// log.Println("DEBUG: Input reader waiting for input...")
		input, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				log.Println("Input stream closed (EOF).")
			} else {
				log.Printf("Error reading input: %v", err)
			}
			// 通知退出
			select {
			case <-gs.quitChan:
			default:
				close(gs.quitChan)
			}
			return
		}
		// log.Printf("DEBUG: Read input: %s", input)
		// 发送到 channel
		select {
		case gs.inputChan <- strings.TrimSpace(input):
			// log.Printf("DEBUG: Sent input to inputChan: %s", strings.TrimSpace(input))
		case <-gs.quitChan:
			log.Println("Input reader shutting down while sending to channel.")
			return
		}
	}
}

// 处理网络消息 (在主循环中调用)
func (gs *GameState) handleNetworkMessage(msg Message) {
	// log.Printf("DEBUG: Handling network message: %+v\n", msg)
	var opponentMoved = false
	var chatReceived = false
	var stateChanged = false
	var senderName = fmt.Sprintf("Player %d", msg.Player) // 默认显示对方编号

	gs.mu.Lock()     //加锁保护状态修改
	if gs.gameOver { // 如果游戏已经结束，不再处理大部分消息
		gs.mu.Unlock()
		if msg.Type == MsgTypeChat { // 但仍然可以接收聊天消息
			gs.AddChatMessage(senderName, msg.Content)
			chatReceived = true
		} else {
			log.Printf("INFO: Ignoring message type %s because game is over.", msg.Type)
		}
		// return // 如果不处理聊天，可以直接返回
	} else { // 游戏进行中
		switch msg.Type {
		case MsgTypeMove:
			if msg.Player != gs.playerID && gs.currentPlayer == msg.Player {
				valid := gs.placePieceInternal(msg.X, msg.Y, msg.Player)
				if valid {
					opponentMoved = true // 标记对方移动成功
					// 检查对方是否获胜或平局
					if checkWinLogic(gs.board, msg.Player) {
						gs.winner = msg.Player
						gs.gameOver = true
						stateChanged = true
					} else if checkDrawLogic(gs.board) {
						gs.winner = 3 // 平局
						gs.gameOver = true
						stateChanged = true
					} else {
						gs.currentPlayer = gs.playerID // 轮到自己
						stateChanged = true
					}
				} else {
					log.Printf("Received invalid move from opponent: (%d, %d)", msg.X, msg.Y)
					// 可以选择发送错误消息回去
					gs.mu.Unlock() // 发送消息前解锁
					gs.SendMessage(Message{Type: MsgTypeError, Content: "Received invalid move"})
					gs.mu.Lock() // 重新锁定以便继续
				}
			} else if msg.Player == gs.playerID {
				// 忽略自己发送的移动回显
			} else {
				log.Printf("WARN: Received move from player %d, but current turn is %d", msg.Player, gs.currentPlayer)
			}
		case MsgTypeChat:
			if msg.Player != gs.playerID { // 只记录和显示对方的消息
				gs.mu.Unlock() // AddChatMessage 有自己的锁
				gs.AddChatMessage(senderName, msg.Content)
				gs.mu.Lock() // 重新锁定
				chatReceived = true
			}
		case MsgTypeState:
			gs.currentPlayer = msg.Turn
			gs.winner = msg.Winner
			gs.gameOver = (msg.Winner != 0)
			stateChanged = true
			if gs.gameOver {
				log.Println("INFO: Received game over state from remote.")
			}
		case MsgTypeAssign:
			if gs.playerID == 0 {
				gs.playerID = msg.Player
				log.Printf("INFO: Assigned player ID: %d\n", gs.playerID)
				stateChanged = true
				// 初始化回合
				if gs.playerID == Player1 {
					gs.currentPlayer = Player1
				} else {
					gs.currentPlayer = Player1 // 客户端等待P1先手
				}
			}
		case MsgTypeError:
			log.Printf("Received error from opponent: %s", msg.Content)
			// 可能需要根据错误类型设置 gameOver
			stateChanged = true // 至少日志变了，可能需要重绘
		case MsgTypeNotify:
			// 可以用来处理一些不需要锁的操作或简单通知
			log.Printf("INFO: Received notification: %s", msg.Content)
			stateChanged = true // 可能需要重绘以显示通知或日志
		default:
			log.Printf("Received unknown message type: %s", msg.Type)
		}
	}
	gs.mu.Unlock() // 解锁

	// 根据处理结果，决定是否需要重绘屏幕
	if opponentMoved || chatReceived || stateChanged {
		gs.SetNeedsRedraw()
	}
	if gs.gameOver {
		// 游戏结束后，确保通知所有 goroutine 退出
		select {
		case <-gs.quitChan:
		default:
			close(gs.quitChan)
		}
	}
}

// 处理用户输入 (在主循环中调用)
func (gs *GameState) handleUserInput(input string) {
	gs.mu.Lock() // 需要读取 playerID 和 currentPlayer
	myPlayerID := gs.playerID
	myTurn := (myPlayerID != 0) && (gs.currentPlayer == myPlayerID) && !gs.gameOver
	isGameOver := gs.gameOver // Read game over state too
	gs.mu.Unlock()

	if myPlayerID == 0 {
		fmt.Println("Still waiting for player assignment. Input ignored.")
		gs.SetNeedsRedraw() // Need to redraw to potentially clear the invalid input prompt
		return
	}

	if isGameOver {
		fmt.Println("Game is over. Input ignored.")
		gs.SetNeedsRedraw()
		return
	}

	if !myTurn {
		fmt.Println("It's not your turn.")
		gs.SetNeedsRedraw() // 可能需要重绘以清除输入提示
		return
	}

	var messageToSend *Message = nil // 指针，以便知道是否需要发送
	var localChatMsg string = ""     // 用于本地显示自己的聊天

	if strings.HasPrefix(input, "/c ") {
		// --- 处理聊天输入 ---
		chatMsg := strings.TrimPrefix(input, "/c ")
		if chatMsg != "" {
			localChatMsg = chatMsg // 记录本地消息内容
			messageToSend = &Message{
				Type:    MsgTypeChat,
				Player:  myPlayerID,
				Content: chatMsg,
			}
		}
	} else {
		// --- 处理移动输入 ---
		parts := strings.Split(input, ",")
		if len(parts) == 2 {
			xStr, yStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			x, errX := strconv.Atoi(xStr)
			y, errY := strconv.Atoi(yStr)

			if errX == nil && errY == nil {
				var validMove, win, draw bool
				var nextPlayer int

				gs.mu.Lock()                                        // --- 开始临界区 ---
				if gs.currentPlayer == myPlayerID && !gs.gameOver { // 再次检查，防止状态变化
					validMove = gs.placePieceInternal(x, y, myPlayerID)
					if validMove {
						win = checkWinLogic(gs.board, myPlayerID)
						draw = checkDrawLogic(gs.board)
						if win {
							gs.winner = myPlayerID
							gs.gameOver = true
						} else if draw {
							gs.winner = 3
							gs.gameOver = true
						} else {
							gs.currentPlayer = 3 - myPlayerID // 切换回合
							nextPlayer = gs.currentPlayer
						}
					}
				}
				gs.mu.Unlock() // --- 结束临界区 ---

				if validMove {
					// 准备发送移动消息
					messageToSend = &Message{
						Type:   MsgTypeMove,
						Player: myPlayerID,
						X:      x,
						Y:      y,
					}
					gs.SetNeedsRedraw() // 自己移动了，需要重绘

					// 如果游戏因这次移动而结束，也发送最终状态
					if win || draw {
						log.Println("INFO: Game over after my move.")
						go gs.SendMessage(Message{Type: MsgTypeState, Winner: gs.winner, Turn: 0}) // 异步发送结束状态
						// 确保退出
						select {
						case <-gs.quitChan:
						default:
							close(gs.quitChan)
						}
					} else {
						log.Printf("INFO: My move successful, next turn: Player %d\n", nextPlayer)
					}
				} else {
					fmt.Println("Invalid move. Try again.")
					gs.SetNeedsRedraw()
				}
			} else {
				fmt.Println("Invalid input format. Use x,y for moves (e.g., 7,7).")
				gs.SetNeedsRedraw()
			}
		} else {
			fmt.Println("Invalid input format. Use x,y for moves or /c <message>.")
			gs.SetNeedsRedraw()
		}
	}

	// 发送消息（如果需要） - 在锁外执行
	if messageToSend != nil {
		go gs.SendMessage(*messageToSend) // 异步发送，避免阻塞主循环
	}

	// 如果是本地聊天消息，添加到聊天记录 - 在锁外执行
	if localChatMsg != "" {
		gs.AddChatMessage(fmt.Sprintf("You (Player %d)", myPlayerID), localChatMsg)
		gs.SetNeedsRedraw() // 需要重绘聊天区
	}
}

// --- 主程序逻辑 ---

func main() {
	listenAddr := flag.String("listen", "", "Address to listen on (e.g., :8080) to run as server")
	connectAddr := flag.String("connect", "", "Address to connect to (e.g., localhost:8080) to run as client")
	flag.Parse()

	gs := &GameState{
		board:          NewBoard(BoardSize),
		currentPlayer:  0, // 等待分配
		winner:         0,
		gameOver:       false,
		playerID:       0,
		chatHistory:    make([]string, 0),
		needsRedraw:    true,                   // 初始需要绘制
		inputChan:      make(chan string, 1),   // 带缓冲，避免输入时阻塞发送者
		networkMsgChan: make(chan Message, 10), // 带缓冲，处理突发消息
		quitChan:       make(chan struct{}),    // 用于关闭信号
	}

	var listener net.Listener
	var conn net.Conn
	var err error

	// --- 设置网络连接 ---
	isServer := false
	if *listenAddr != "" {
		isServer = true
		fmt.Println("Starting server on", *listenAddr)
		listener, err = net.Listen("tcp", *listenAddr)
		if err != nil {
			log.Fatalf("Failed to listen: %v", err)
		}
		defer listener.Close()
		fmt.Println("Waiting for opponent to connect...")
		conn, err = listener.Accept()
		if err != nil {
			log.Fatalf("Failed to accept connection: %v", err)
		}
		fmt.Println("Opponent connected from", conn.RemoteAddr())
	} else if *connectAddr != "" {
		fmt.Println("Connecting to server at", *connectAddr)
		conn, err = net.DialTimeout("tcp", *connectAddr, 10*time.Second)
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		fmt.Println("Connected to server.")
	} else {
		fmt.Println("Please specify either --listen <addr> or --connect <addr>")
		os.Exit(1)
	}
	fmt.Println("Connection established.")
	gs.conn = conn // 保存连接
	gs.encoder = json.NewEncoder(conn)
	gs.decoder = json.NewDecoder(conn)
	defer gs.conn.Close() // 确保连接最终关闭

	// 启动 I/O goroutines
	go gs.networkReceiver()
	go gs.inputReader()

	// --- 初始化玩家 (服务器发送分配) ---
	if isServer {
		gs.mu.Lock()
		gs.playerID = Player1
		gs.currentPlayer = Player1 // 服务器先手
		gs.mu.Unlock()
		fmt.Println("You are Player 1 (X). Your turn.")
		assignMsg := Message{Type: MsgTypeAssign, Player: Player2}
		go gs.SendMessage(assignMsg) // 异步发送分配消息
		gs.SetNeedsRedraw()
	} else {
		fmt.Println("Waiting for player assignment from server...")
		// 等待 Assign 消息在主循环中处理
	}

	// --- 主事件循环 ---
	ticker := time.NewTicker(100 * time.Millisecond) // 定期检查重绘
	defer ticker.Stop()

	running := true
	for running {

		// 检查是否需要重绘并执行
		if gs.CheckAndResetRedraw() {
			// 在绘制前获取最新状态 (避免在锁内绘制)
			gs.mu.Lock()
			myPlayerID := gs.playerID
			currentTurnPlayer := gs.currentPlayer
			isMyTurn := (currentTurnPlayer == myPlayerID) && !gs.gameOver
			isGameOver := gs.gameOver
			gs.mu.Unlock()

			// 清屏或滚动以显示最新状态
			fmt.Print("\033[H\033[2J") // ANSI 清屏 - 可选

			gs.DisplayBoard()
			gs.DisplayChat()

			if isGameOver {
				gs.mu.Lock()
				winner := gs.winner
				gs.mu.Unlock()
				fmt.Println("--- GAME OVER ---")
				switch winner {
				case Player1:
					fmt.Println("Player 1 (X) wins!")
				case Player2:
					fmt.Println("Player 2 (O) wins!")
				case 3:
					fmt.Println("It's a draw!")
				default:
					fmt.Println("Game ended.") // 可能因断线
				}
				fmt.Println("Press Ctrl+C or close the window to exit.")
				// running = false // 可以直接在这里退出循环，或者等待 quitChan
			} else if myPlayerID != 0 { // 确保已分配 ID
				if isMyTurn {
					fmt.Printf("Your turn (Player %d). Enter move (x,y) or chat (/c message): ", myPlayerID)
				} else {
					fmt.Printf("Waiting for Player %d's move...\n", currentTurnPlayer)
				}
			} else {
				fmt.Println("Connecting and waiting for player assignment...")
			}
		}

		// 使用 select 处理不同的事件源
		select {
		case input := <-gs.inputChan:
			// log.Println("DEBUG: Main loop received input:", input)
			if input != "" { // 忽略空输入
				gs.handleUserInput(input)
			} else {
				gs.SetNeedsRedraw() // 空输入也可能需要重置提示
			}

		case msg := <-gs.networkMsgChan:
			// log.Println("DEBUG: Main loop received network message:", msg.Type)
			gs.handleNetworkMessage(msg)

		case <-ticker.C:
			// 定期检查，主要是为了在没有其他事件时也能触发重绘检查
			// log.Println("DEBUG: Tick.") // 非常频繁，调试时才打开
			continue // 继续循环以检查 needsRedraw

		case <-gs.quitChan:
			fmt.Println("\nReceived quit signal. Exiting main loop.")
			running = false // 退出循环
		}
	} // end main loop

	fmt.Println("Shutting down.")
	// (连接已通过 defer 关闭)
	// 等待用户查看最终信息
	time.Sleep(2 * time.Second) // 短暂等待，让用户看到结束信息
}
