proc setdef { var val } {
    if {![uplevel "info exists $var"]} { uplevel "set $var \"$val\"" }
}
