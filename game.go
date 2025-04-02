package main

import (
	"fmt"
	"strings"
)

// 定义玩家和棋盘状态
const (
	PlayerX = "X"
	PlayerO = "O"
	Empty   = " "
)

// 定义游戏状态
type GameState int

const (
	StatePlaying GameState = iota
	StateWon
	StateDraw
)

// TicTacToe 游戏结构
type TicTacToe struct {
	board         [3][3]string
	currentPlayer string
	state         GameState
	winner        string
}

// NewGame 创建一个新的游戏实例
func NewGame() *TicTacToe {
	g := &TicTacToe{
		currentPlayer: PlayerX, // X 先开始
		state:         StatePlaying,
	}
	// 初始化空棋盘
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			g.board[i][j] = Empty
		}
	}
	return g
}

// GetBoard 返回当前棋盘状态 (副本)
func (g *TicTacToe) GetBoard() [3][3]string {
	return g.board
}

// GetCurrentPlayer 返回当前玩家
func (g *TicTacToe) GetCurrentPlayer() string {
	return g.currentPlayer
}

// GetState 返回当前游戏状态
func (g *TicTacToe) GetState() GameState {
	return g.state
}

// GetWinner 返回获胜者 (如果游戏结束且有赢家)
func (g *TicTacToe) GetWinner() string {
	return g.winner
}

// MakeMove 尝试在指定位置下棋
func (g *TicTacToe) MakeMove(row, col int) error {
	if g.state != StatePlaying {
		return fmt.Errorf("游戏已经结束")
	}
	if row < 0 || row >= 3 || col < 0 || col >= 3 {
		return fmt.Errorf("无效的位置: (%d, %d)，请输入 1-3 之间的数字", row+1, col+1)
	}
	if g.board[row][col] != Empty {
		return fmt.Errorf("无效的位置: (%d, %d) 已经被占据", row+1, col+1)
	}

	g.board[row][col] = g.currentPlayer
	g.updateState()

	// 如果游戏仍在进行，切换玩家
	if g.state == StatePlaying {
		if g.currentPlayer == PlayerX {
			g.currentPlayer = PlayerO
		} else {
			g.currentPlayer = PlayerX
		}
	}

	return nil
}

// updateState 检查游戏是否结束 (胜利或平局)
func (g *TicTacToe) updateState() {
	// 检查行、列、对角线是否有赢家
	if g.checkWinCondition(PlayerX) {
		g.state = StateWon
		g.winner = PlayerX
		return
	}
	if g.checkWinCondition(PlayerO) {
		g.state = StateWon
		g.winner = PlayerO
		return
	}

	// 检查是否平局 (所有格子都填满)
	isDraw := true
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if g.board[i][j] == Empty {
				isDraw = false
				break
			}
		}
		if !isDraw {
			break
		}
	}
	if isDraw {
		g.state = StateDraw
		g.winner = "" // 平局没有赢家
	}
}

// checkWinCondition 检查指定玩家是否获胜
func (g *TicTacToe) checkWinCondition(player string) bool {
	// 检查行
	for i := 0; i < 3; i++ {
		if g.board[i][0] == player && g.board[i][1] == player && g.board[i][2] == player {
			return true
		}
	}
	// 检查列
	for j := 0; j < 3; j++ {
		if g.board[0][j] == player && g.board[1][j] == player && g.board[2][j] == player {
			return true
		}
	}
	// 检查对角线
	if g.board[0][0] == player && g.board[1][1] == player && g.board[2][2] == player {
		return true
	}
	if g.board[0][2] == player && g.board[1][1] == player && g.board[2][0] == player {
		return true
	}
	return false
}

// displayBoard 在终端打印棋盘
func displayBoard(board [3][3]string) {
	clearScreen()          // 清屏让界面更干净
	fmt.Println("  1 2 3") // 列号
	for i := 0; i < 3; i++ {
		fmt.Printf("%d %s\n", i+1, strings.Join(board[i][:], "|")) // 行号和内容
		if i < 2 {
			fmt.Println("  -----") // 分隔线
		}
	}
	fmt.Println()
}

// clearScreen 清除终端屏幕 (跨平台)
func clearScreen() {
	// 简单的清屏方法，可能不适用于所有终端
	// 对于 Windows: 使用 "cls"
	// 对于 Linux/macOS: 使用 "clear"
	// 更通用的方法是使用 ANSI 转义码
	fmt.Print("\033[H\033[2J")
}
