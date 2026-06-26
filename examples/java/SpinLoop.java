// Long-running sample used for testing `sl-dbg pause`.
// Compile: javac -g examples/java/SpinLoop.java
// Run:     java -agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5005 \
//               -cp examples/java SpinLoop
public class SpinLoop {
    public static void main(String[] args) throws InterruptedException {
        long counter = 0;
        while (true) {
            counter++;
            if (counter % 1000000 == 0) {
                Thread.sleep(50);
            }
        }
    }
}
