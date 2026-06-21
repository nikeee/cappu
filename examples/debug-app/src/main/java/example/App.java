package example;

public class App {
    public static void main(String[] args) {
        int sum = 0;
        for (int i = 1; i <= 3; i++) {
            int squared = i * i;
            sum += squared; // a debugger stops here: i, squared and sum are all in scope
        }
        System.out.println("sum=" + sum);
    }
}
