package example;

import org.mapstruct.Mapper;
import org.mapstruct.Mapping;

@Mapper
public interface CarMapper {
    @Mapping(target = "name", source = "model")
    @Mapping(target = "horsePower", source = "power")
    CarDto toDto(Car car);
}
