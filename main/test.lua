local rules = r{
$ a:
    echo hi
}

print(b{
$ %.o: %.c
    gcc $input -o $output
rules
})

sub = include("sub/sub.lua")
print(sub)

print(dcall(foo))

print(expand"$su")

return rules
