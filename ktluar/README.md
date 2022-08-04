# ktluar [![GoDoc](https://godoc.org/layeh.com/ktluar?status.svg)](https://godoc.org/layeh.com/ktluar)

ktluar simplifies data passing to and from [gopher-lua](https://github.com/zyedidia/knit/ktlua).

Example usage:

```go
package luar_test

import (
    "fmt"

    "github.com/zyedidia/knit/ktlua"
    "layeh.com/ktluar"
)

type User struct {
    Name  string
    token string
}

func (u *User) SetToken(t string) {
    u.token = t
}

func (u *User) Token() string {
    return u.token
}

const script = `
print("Hello from Lua, " .. u.Name .. "!")
u:SetToken("12345")
`

func Example_basic() {
    L := lua.NewState()
    defer L.Close()

    u := &User{
        Name: "Tim",
    }
    L.SetGlobal("u", luar.New(L, u))
    if err := L.DoString(script); err != nil {
        panic(err)
    }

    fmt.Println("Lua set your token to:", u.Token())
    // Output:
    // Hello from Lua, Tim!
    // Lua set your token to: 12345
}
```

## License

MPL 2.0

## Author

Tim Cooper (<tim.cooper@layeh.com>)
