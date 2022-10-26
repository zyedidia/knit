gcc -Wall -O2 -c hello.c -o hello.o
gcc -Wall -O2 -c other.c -o other.o
gcc -Wall -O2 hello.o other.o -o hello
