proc setdef { var val } {
    if {![uplevel "info exists $var"]} { uplevel "set $var \"$val\"" }
}

proc let {varName eq args} {
    upvar $varName v
    if {$eq == "="} {
        set v [expr $args]
    } else {
        if {$eq == "?="} {
            setdef v [expr $args]
        }
    }
}
