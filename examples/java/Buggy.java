// A deliberately buggy example program for sl-dbg integration tests.
//
// Build:
//     javac -g examples/java/Buggy.java
//
// Run (attach mode):
//     java -agentlib:jdwp=transport=dt_socket,server=y,suspend=y,address=*:5005 \
//          -cp examples/java Buggy
//
// In another shell:
//     sl-dbg attach --lang java --host localhost --port 5005
//     sl-dbg break examples/java/Buggy.java:22 --if "item < 0"
//     sl-dbg continue
//     sl-dbg locals
public class Buggy {
    static int compute(int item) {
        // Bug: ArithmeticException when item is 0; silently returns negative on item < 0.
        return item == 0 ? 0 : 1000 / item;
    }

    static int process(int[] items) {
        int total = 0;
        for (int item : items) {        // line 21
            int x = compute(item);      // line 22
            total += x;                 // line 23
        }
        return total;
    }

    public static void main(String[] args) throws InterruptedException {
        int[] data = { 10, 5, 2, 0, -1, 4 };
        int result = process(data);
        System.out.println("result=" + result);
    }
}
